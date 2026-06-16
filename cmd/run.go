package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/depado/buoy/internal/backup"
	"github.com/depado/buoy/internal/docker"
	"github.com/depado/buoy/internal/hook"
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

		dockerClient, err := docker.New(conf.Docker.Host)
		if err != nil {
			return err
		}
		defer dockerClient.Close()

		if _, err := os.Stat(conf.Restic.BinaryPath); os.IsNotExist(err) {
			return fmt.Errorf("restic binary not found: %s", conf.Restic.BinaryPath)
		}

		resticClient := restic.New(conf.Restic.BinaryPath, conf.Restic.Password)
		hookExec := hook.New(dockerClient)
		runner := backup.New(dockerClient, resticClient, hookExec, conf.Restic.BaseRepo, logger)
		sched := scheduler.New(dockerClient, runner, conf.Daemon.Concurrency, logger)

		containers, err := dockerClient.ListBackupContainers(context.Background())
		if err != nil {
			return err
		}
		for i := range containers {
			ctr := &containers[i]

			cfg := docker.ParseBackupConfig(ctr.Labels)
			repo := cfg.RepoOverride
			if repo == "" {
				repo = ctr.RepoPath(conf.Restic.BaseRepo)
			}

			exists, err := resticClient.RepoExists(context.Background(), repo)
			if err != nil {
				logger.Warn("failed to check repo, skipping container", "container", ctr.Name, "repo", repo, "error", err)
				continue
			}
			if !exists {
				if err := resticClient.Init(context.Background(), repo); err != nil {
					logger.Warn("failed to init repo", "container", ctr.Name, "repo", repo, "error", err)
					continue
				}
				logger.Info("initialized repo", "container", ctr.Name, "repo", repo)
			}
			if err := sched.AddContainer(ctr); err != nil {
				logger.Warn("failed to schedule container", "container", ctr.Name, "error", err)
			}
		}

		watcher := docker.NewWatcher(dockerClient, logger)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		events, errs := watcher.Watch(ctx)

		sched.Start()

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

		for {
			select {
			case evt := <-events:
				switch evt.Type {
				case docker.EventStart:
					ctr, err := dockerClient.InspectContainer(context.Background(), evt.ID)
					if err != nil {
						logger.Warn("failed to inspect on event", "id", evt.ID, "error", err)
						continue
					}
					cfg := docker.ParseBackupConfig(ctr.Labels)
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
			case sig := <-sigs:
				logger.Info("received signal, shutting down", "signal", sig)
				cancel()
				done := sched.Stop()
				<-done.Done()
				return nil
			}
		}
	},
}
