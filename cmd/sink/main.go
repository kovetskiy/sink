package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/docopt/docopt-go"
	"github.com/fsnotify/fsnotify"
	"github.com/kovetskiy/lorg"
	"github.com/reconquest/cog"
	"github.com/reconquest/karma-go"
	"github.com/reconquest/lexec-go"
)

var (
	version = "[manual build]"
	usage   = "sink " + version + os.ExpandEnv(`

Two way synchronizer with git and inotify.

Usage:
   sink [options]
   sink -h | --help
   sink --version

Options:
  -d --dir <path>       Path of guts to sync. [default: $HOME/.guts/]
  -i --interval <path>  Interval between syncs in seconds. [default: 60]
  -k --ssh-key <path>   Path to SSH key. [default: $HOME/.ssh/id_rsa].
  -s --sync             Quit after initial sync.
  --trace               Enable trace messages.
  -h --help             Show this screen.
  --version             Show version.
`)
)

var (
	logger   *cog.Logger
	hostname string
)

var ErrRejected = errors.New("push rejected")

func init() {
	stderr := lorg.NewLog()
	stderr.SetIndentLines(true)
	stderr.SetFormat(
		lorg.NewFormat("${time} ${level:[%s]:right:short} ${prefix}%s"),
	)

	logger = cog.NewLogger(stderr)
}

func main() {
	args, err := docopt.Parse(usage, nil, true, version, false)
	if err != nil {
		panic(err)
	}

	directory := args["--dir"].(string)

	interval, err := strconv.ParseInt(args["--interval"].(string), 10, 64)
	if err != nil {
		logger.Fatalf(err, "unable to parse interval")
	}

	if args["--trace"].(bool) {
		logger.SetLevel(lorg.LevelTrace)
	}

	hostname, err = os.Hostname()
	if err != nil {
		logger.Fatalf(err, "unable to get hostname")
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Fatalf(err, "unable to spawn new watcher")
	}

	done := make(chan struct{})
	triggers := make(chan struct{}, 1)

	go handelFileSystemEvents(directory, watcher, triggers, done)
	go handleSyncTriggers(directory, args["--ssh-key"].(string), interval, triggers)

	logger.Tracef(nil, "syncing directory: %s")

	for {
		err = sync(directory, args["--ssh-key"].(string), true)
		if err != nil {
			logger.Errorf(
				err,
				"unable to synchronize directory: %s",
				directory,
			)

			if args["--sync"].(bool) {
				continue
			}
		}

		break
	}

	if args["--sync"].(bool) {
		os.Exit(0)
	}

	logger.Infof(nil, "watching for changes in directory: %s", directory)
	watcher.Add(directory)

	<-done
}

func handleSyncTriggers(
	directory string,
	sshKey string,
	interval int64,
	triggers chan struct{},
) {
	for {
		select {
		case <-triggers:
			err := sync(directory, sshKey, false)
			if err != nil {
				if err != ErrRejected {
					logger.Errorf(
						err,
						"unable to synchronize directory: %s",
						directory,
					)
				}

				trigger(triggers)
			}

			if interval > 0 {
				logger.Tracef(nil, "sleeping %v seconds", interval)

				time.Sleep(time.Duration(interval) * time.Second)
			}
		}
	}
}

func handelFileSystemEvents(
	directory string,
	watcher *fsnotify.Watcher,
	triggers chan struct{},
	done chan struct{},
) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				close(done)
				return
			}

			logger.Tracef(nil, "event: %v", event)

			trigger(triggers)

		case err, ok := <-watcher.Errors:
			if err != nil {
				logger.Errorf(err, "error while watching directory: %s", directory)
			}

			if !ok {
				close(done)
				return
			}
		}
	}
}

func trigger(triggers chan struct{}) {
	logger.Tracef(nil, "got event, triggering sync event")
	select {
	case triggers <- struct{}{}:
	default:
		logger.Tracef(nil, "some trigger event already in the queue")
		// syncer is already running and changes will be synced too
	}
}

func sync(directory string, sshKey string, withPrints bool) error {
	logger.Tracef(nil, "syncing directory: %s", directory)

	if withPrints {
		logger.Infof(nil, "synchronizing contents of directory %s", directory)
	}

	cmd := gitCommand(GitCommandArgs{
		Directory: directory,
		SSHKey:    sshKey,
	}, "add", ".")
	err := cmd.Run()
	if err != nil {
		return karma.Format(
			err,
			"unable to git add changes",
		)
	}

	cmd = gitCommand(
		GitCommandArgs{
			Directory: directory,
			SSHKey:    sshKey,
		},
		"commit", "-m", hostname+": automatic commit",
	)

	stdout, _, err := cmd.Output()
	if err != nil {
		if !bytes.Contains(
			stdout,
			[]byte("nothing to commit, working tree clean"),
		) {
			return karma.Format(
				err,
				"unable to git commit changes",
			)
		}
	}

	if withPrints {
		logger.Infof(nil, "fetching remote repository")
	}

	cmd = gitCommand(GitCommandArgs{
		Directory: directory,
		SSHKey:    sshKey,
	}, "remote", "update")
	err = cmd.Run()
	if err != nil {
		return karma.Format(
			err,
			"unable to update remote",
		)
	}

	if withPrints {
		logger.Infof(nil, "merging remote changes to local master branch")
	}

	cmd = gitCommand(GitCommandArgs{
		Directory: directory,
		SSHKey:    sshKey,
	}, "merge", "--no-commit", "origin/master")
	err = cmd.Run()
	if err != nil {
		return karma.Format(
			err,
			"unable to pull repository changes",
		)
	}

	if withPrints {
		logger.Infof(nil, "pushing local changes to remote repository")
	}

	cmd = gitCommand(GitCommandArgs{
		Directory: directory,
		SSHKey:    sshKey,
	}, "push", "origin", "master")
	err = cmd.Run()
	if err != nil {
		if strings.Contains(err.Error(), "[rejected]") {
			return ErrRejected
		}

		return karma.Format(
			err,
			"unable to push changes",
		)
	}

	return nil
}

type GitCommandArgs struct {
	SSHKey    string
	Directory string
}

func gitCommand(
	args GitCommandArgs,
	values ...string,
) *lexec.Execution {
	cmdline := exec.Command(
		"git",
		append([]string{"-C", args.Directory}, values...)...,
	)

	cmdline.Env = os.Environ()

	cmdline.Env = append(
		cmdline.Env,
		fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s", args.SSHKey),
	)

	cmd := lexec.NewExec(
		lexec.Loggerf(logger.Log.Debugf),
		cmdline,
	)

	return cmd
}
