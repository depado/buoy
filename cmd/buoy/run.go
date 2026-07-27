package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/depado/buoy/internal/api"
	"github.com/depado/buoy/internal/backup"
	"github.com/depado/buoy/internal/config"
	"github.com/depado/buoy/internal/docker"
	"github.com/depado/buoy/internal/hook"
	"github.com/depado/buoy/internal/notify"
	"github.com/depado/buoy/internal/registry"
	"github.com/depado/buoy/internal/restic"
	"github.com/depado/buoy/internal/scheduler"
	"github.com/depado/buoy/internal/telemetry"
	"github.com/depado/buoy/internal/version"
)

var RunCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the buoy daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := conf.Restic.Validate(); err != nil {
			return err
		}
		logger := config.NewLogger(&conf)
		slog.SetDefault(logger)
		logger.Info("starting buoy daemon", "version", version.Version)

		repoRefs, repoLogList := config.ToRepoRefs(conf.Restic.Repos)

		logger.Info("configuration",
			"concurrency", conf.Daemon.Concurrency,
			"binary", conf.Restic.BinaryPath,
			"repos", repoLogList,
			"compression", conf.Restic.Compression,
			"default_schedule", conf.Daemon.DefaultSchedule,
			"default_retention", conf.Daemon.DefaultRetention,
			"resync_interval", conf.Daemon.ResyncInterval,
			"check_schedule", conf.Daemon.CheckSchedule,
			"notify_level", conf.Notify.Level,
			"db_path", conf.Daemon.DBPath,
		)

		tel, telErr := telemetry.New(&conf, logger)
		if telErr != nil {
			logger.Warn("telemetry setup failed, continuing without", "error", telErr)
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := tel.Shutdown(shutdownCtx); err != nil {
				logger.Warn("telemetry shutdown error", "error", err)
			}
		}()

		logger = slog.New(tel.LoggerHandler(logger.Handler()))
		slog.SetDefault(logger)

		reg, err := registry.Open(conf.Daemon.DBPath, repoRefs, logger)
		if err != nil {
			return fmt.Errorf("open registry: %w", err)
		}
		defer reg.Close()

		dockerClient, err := docker.New(conf.Docker.Host)
		if err != nil {
			return err
		}
		defer dockerClient.Close() //nolint:errcheck

		if _, err := exec.LookPath(conf.Restic.BinaryPath); err != nil {
			return fmt.Errorf("restic binary not found: %s", conf.Restic.BinaryPath)
		}

		resticClient := restic.New(conf.Restic.BinaryPath, conf.Restic.Password, conf.Restic.Compression)
		hookExec := hook.New(dockerClient, logger)
		notifier, err := notify.New(conf.Notify.Urls, notify.ParseLevel(conf.Notify.Level), logger)
		if err != nil {
			return fmt.Errorf("notify: %w", err)
		}
		backupTimeout := parseDurationOrDefault(conf.Daemon.BackupTimeout, 1*time.Hour)
		execTimeout := parseDurationOrDefault(conf.Daemon.ExecTimeout, 5*time.Minute)
		healthWaitTimeout := parseDurationOrDefault(conf.Daemon.HealthWaitTimeout, 5*time.Minute)
		ignoredIDs := &sync.Map{}

		runner := backup.New(&backup.RunnerConfig{
			Docker:            dockerClient,
			Restic:            resticClient,
			Hook:              hookExec,
			Registry:          reg,
			ResticConf:        &conf.Restic,
			DefaultSchedule:   conf.Daemon.DefaultSchedule,
			DefaultRetention:  conf.Daemon.DefaultRetention,
			IgnoredIDs:        ignoredIDs,
			Notifier:          notifier,
			Logger:            logger,
			ExecTimeout:       execTimeout,
			HealthWaitTimeout: healthWaitTimeout,
			BackupTimeout:     backupTimeout,
			Meters:            tel.Meters(),
			Tracers:           tel.Tracers(),
		})
		sched := scheduler.New(&scheduler.Config{
			Docker:           dockerClient,
			Runner:           runner,
			Registry:         reg,
			Concurrency:      conf.Daemon.Concurrency,
			DefaultSchedule:  conf.Daemon.DefaultSchedule,
			DefaultRetention: conf.Daemon.DefaultRetention,
			BackupTimeout:    backupTimeout,
			Logger:           logger,
			Tracer:           tel.Tracers().Tracer,
		})

		var apiSrv *api.Server
		if conf.API.Enabled {
			apiSrv = api.New(reg, resticClient, sched, &conf.Restic, conf.API.Token, conf.API.Host, conf.API.Port, version.Version, logger, sched.Running)
			go func() {
				if err := apiSrv.Start(); err != nil && err != http.ErrServerClosed {
					logger.Error("api server error", "error", err)
				}
			}()
		}

		if err := func() error {
			ctx, span := tel.Tracers().Tracer.Start(context.Background(), "buoy.startup_scan")
			defer span.End()
			containers, scanErr := dockerClient.ListBackupContainers(ctx)
			if scanErr != nil {
				return scanErr
			}
			scheduled := 0
			for i := range containers {
				ctr := &containers[i]

				if err := sched.AddContainer(ctr); err != nil {
					logger.Warn("failed to schedule container", "container", ctr.Name, "container_id", ctr.ID, "error", err)
				} else {
					scheduled++
				}
			}
			logger.Info("startup scan complete", "containers", len(containers), "scheduled", scheduled, "stacks", countStacks(containers))
			return nil
		}(); err != nil {
			return err
		}

		watcher := docker.NewWatcher(dockerClient, logger)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		events, errs := watcher.Watch(ctx)

		if conf.Daemon.CheckSchedule != "" {
			if err := sched.ScheduleCheck(conf.Daemon.CheckSchedule); err != nil {
				logger.Warn("failed to schedule periodic check", "error", err)
			} else {
				logger.Info("scheduled periodic restic check", "schedule", conf.Daemon.CheckSchedule)
			}
		}

		sched.Start()

		var resyncTicker <-chan time.Time
		if resyncInterval := parseDurationOrDefault(conf.Daemon.ResyncInterval, 0); resyncInterval > 0 {
			t := time.NewTicker(resyncInterval)
			defer t.Stop()
			resyncTicker = t.C
		}

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

		for {
			select {
			case evt := <-events:
				if _, own := ignoredIDs.Load(evt.ID); own {
					logger.Debug("ignoring self-triggered event", "type", evt.Type, "name", evt.ActorName)
					continue
				}
				logger.Debug("received event", "type", evt.Type, "name", evt.ActorName)
				switch evt.Type {
				case docker.EventStart:
					ctx, span := tel.Tracers().Tracer.Start(context.Background(), "buoy.container.detected",
						trace.WithAttributes(
							attribute.String("container.name", evt.ActorName),
						),
					)
					ctr, err := dockerClient.InspectContainer(ctx, evt.ID)
					if err != nil {
						logger.Warn("failed to inspect on event", "id", evt.ID, "error", err)
						span.SetStatus(codes.Error, err.Error())
						span.End()
						continue
					}
					if err := sched.AddContainer(ctr); err != nil {
						logger.Warn("failed to schedule on event", "container", ctr.Name, "container_id", ctr.ID, "error", err)
					}
					span.End()
				case docker.EventDie, docker.EventDestroy:
					sched.RemoveContainer(evt.ID)
				}
			case err := <-errs:
				logger.Warn("docker event stream error", "error", err)
			case <-resyncTicker:
				sched.Resync(ctx)
			case sig := <-sigs:
				if sched.Running() {
					logger.Warn("received signal, backup in progress - waiting for completion", "signal", sig)
				} else {
					logger.Info("received signal, shutting down", "signal", sig)
				}
				cancel()
				if apiSrv != nil {
					shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer shutdownCancel()
					if err := apiSrv.Shutdown(shutdownCtx); err != nil {
						logger.Warn("api server shutdown error", "error", err)
					}
				}
				done := sched.Stop()
				<-done.Done()
				return nil
			}
		}
	},
}

func countStacks(containers []docker.Container) int {
	stacks := make(map[string]bool)
	for _, c := range containers {
		if c.ComposeProject != "" {
			stacks[c.ComposeProject] = true
		}
	}
	return len(stacks)
}

func parseDurationOrDefault(s string, defaultDuration time.Duration) time.Duration {
	if s == "" {
		return defaultDuration
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		slog.Warn("invalid duration, using default", "value", s, "default", defaultDuration)
		return defaultDuration
	}
	return d
}
