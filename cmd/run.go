package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/depado/buoy/internal/backup"
	"github.com/depado/buoy/internal/docker"
	"github.com/depado/buoy/internal/hook"
	"github.com/depado/buoy/internal/notify"
	"github.com/depado/buoy/internal/restic"
	"github.com/depado/buoy/internal/scheduler"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the buoy daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		conf, err := NewConf()
		if err != nil {
			return err
		}

		logger := NewLogger(conf)
		slog.SetDefault(logger)
		logger.Info("starting buoy daemon", "version", Version)

		if conf.Restic.Password == "" {
			return fmt.Errorf("restic.password is required")
		}
		if len(conf.Restic.Repos) == 0 {
			return fmt.Errorf("restic.repos is required")
		}

		logger.Info("configuration",
			"concurrency", conf.Daemon.Concurrency,
			"binary", resticBinary(conf),
			"repos", conf.Restic.Repos,
			"compression", conf.Restic.Compression,
			"default_schedule", conf.Daemon.DefaultSchedule,
			"default_retention", conf.Daemon.DefaultRetention,
			"resync_interval", conf.Daemon.ResyncInterval,
			"check_schedule", conf.Daemon.CheckSchedule,
			"notify_level", conf.Notify.Level,
		)

		dockerClient, err := docker.New(conf.Docker.Host)
		if err != nil {
			return err
		}
		defer dockerClient.Close() //nolint:errcheck

		if _, err := exec.LookPath(resticBinary(conf)); err != nil {
			return fmt.Errorf("restic binary not found: %s", resticBinary(conf))
		}

	resticClient := restic.New(resticBinary(conf), conf.Restic.Password, conf.Restic.Compression)
		hookExec := hook.New(dockerClient)
		notifier, err := notify.New(conf.Notify.Urls, notify.ParseLevel(conf.Notify.Level), logger)
		if err != nil {
			return fmt.Errorf("notify: %w", err)
		}
		ignoredIDs := &sync.Map{}
		runner := backup.New(dockerClient, resticClient, hookExec, conf.Restic.Repos,
			conf.Daemon.DefaultSchedule, conf.Daemon.DefaultRetention, ignoredIDs,
			notifier, logger,
			parseDurationOrDefault(conf.Daemon.ExecTimeout, 5*time.Minute),
			parseDurationOrDefault(conf.Daemon.HealthWaitTimeout, 5*time.Minute))
		sched := scheduler.New(dockerClient, runner, conf.Daemon.Concurrency, conf.Daemon.DefaultSchedule, conf.Daemon.DefaultRetention,
			parseDurationOrDefault(conf.Daemon.BackupTimeout, 1*time.Hour), logger)

		containers, err := dockerClient.ListBackupContainers(context.Background())
		if err != nil {
			return err
		}
		scheduled := 0
		for i := range containers {
			ctr := &containers[i]

			if err := sched.AddContainer(ctr); err != nil {
				logger.Warn("failed to schedule container", "container", ctr.Name, "error", err)
			} else {
				scheduled++
			}
		}
		logger.Info("startup scan complete", "containers", len(containers), "scheduled", scheduled, "stacks", countStacks(containers))

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
					ctr, err := dockerClient.InspectContainer(context.Background(), evt.ID)
					if err != nil {
						logger.Warn("failed to inspect on event", "id", evt.ID, "error", err)
						continue
					}
					cfg := docker.ParseBackupConfig(ctr.Labels, "", "", slog.Default())
					if !cfg.Enabled {
						continue
					}
					if err := sched.AddContainer(ctr); err != nil {
						logger.Warn("failed to schedule on event", "container", ctr.Name, "error", err)
					}
				case docker.EventDie, docker.EventDestroy:
					sched.RemoveContainer(evt.ID)
				}
			case err := <-errs:
				logger.Warn("docker event stream error", "error", err)
			case <-resyncTicker:
				sched.Resync(ctx)
			case sig := <-sigs:
				if sched.Running() {
					logger.Warn("received signal, backup in progress — waiting for completion", "signal", sig)
				} else {
					logger.Info("received signal, shutting down", "signal", sig)
				}
				cancel()
				done := sched.Stop()
				<-done.Done()
				return nil
			}
		}
	},
}

func resticBinary(conf *Conf) string {
	if conf.Restic.BinaryPath != "" {
		return conf.Restic.BinaryPath
	}
	return "restic"
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
