package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ledgerwatch/erigon/cmd/datastreamer/checker"
	"github.com/ledgerwatch/erigon/cmd/datastreamer/decoder"
	"github.com/ledgerwatch/erigon/params"
	"github.com/ledgerwatch/erigon/zkevm/log"
	"github.com/urfave/cli/v2"
)

func main() {
	app := cli.NewApp()
	app.Name = "datastreamer"
	app.Version = params.VersionWithCommit(params.GitCommit)

	app.Commands = []*cli.Command{
		&checker.Command,
		&decoder.Command,
	}

	app.UsageText = app.Name + ` [command] [flags]`

	app.Action = func(context *cli.Context) error {
		if context.Args().Present() {
			var goodNames []string
			for _, c := range app.VisibleCommands() {
				goodNames = append(goodNames, c.Name)
			}
			_, _ = fmt.Fprintf(os.Stderr, "Command '%s' not found. Available commands: %s\n", context.Args().First(), goodNames)
			cli.ShowAppHelpAndExit(context, 1)
		}

		return nil
	}

	for _, command := range app.Commands {
		command.Before = func(ctx *cli.Context) error {
			var cancel context.CancelFunc

			ctx.Context, cancel = context.WithCancel(context.Background())

			go handleTerminationSignals(cancel)

			return nil
		}
	}

	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// handleTerminationSignals handles termination signals
func handleTerminationSignals(stopFunc func()) {
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGTERM, syscall.SIGINT)

	switch s := <-signalCh; s {
	case syscall.SIGTERM:
		log.Info("Stopping")
		stopFunc()
	case syscall.SIGINT:
		log.Info("Terminating")
		os.Exit(-int(syscall.SIGINT))
	}
}
