package main

import (
	"bufio"
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/reconquest/karma-go"
	"github.com/reconquest/lexec-go"
	"github.com/reconquest/pkg/log"

	"github.com/docopt/docopt-go"
)

var version = "[manual build]"

var usage = `orgalorg-sink - sync repositories on several machines

Usage:
  orgalorg-sink <path>

Options:
  -h --help  Show this help.
`

var hostname string

func main() {
	args, err := docopt.ParseArgs(usage, nil, "orgalorg-sink "+version)
	if err != nil {
		panic(err)
	}

	var opts struct {
		Path string `docopt:"<path>"`
	}
	err = args.Bind(&opts)
	if err != nil {
		panic(err)
	}

	hostname, err = os.Hostname()
	if err != nil {
		log.Fatalf(err, "unable to get hostname")
	}

	handler := new(Handler)
	handler.dir = opts.Path

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		fields := strings.Fields(line)

		err := handler.Handle(fields)
		if err != nil {
			handler.signal("CRASH")

			log.Fatal(err)
		}
	}
}

type Handler struct {
	dir      string
	prefix   string
	nodes    []string
	ephemera string
	node     string
}

func (handler *Handler) Handle(fields []string) error {
	if len(fields) < 2 {
		return fmt.Errorf("invalid input: %q", fields)
	}

	if handler.prefix == "" {
		handler.prefix = fields[0]
	}

	switch fields[1] {
	case "START":
		return handler.start()

	case "SYNC":
		if len(fields) < 4 {
			return fmt.Errorf("input: %q; expected more fields", fields)
		}

		var data string
		if len(fields) == 5 {
			data = fields[4]
		}

		return handler.serve(fields[2], fields[3], data)

	case "NODE":
		if len(fields) < 2 {
			return fmt.Errorf("input: %q; expected more fields", fields)
		}

		return handler.writeNode(fields[2])
	}

	return fmt.Errorf("unexpected fields: %q", fields)
}

func (handler *Handler) start() error {
	handler.ephemera = fmt.Sprintf("%v%v", time.Now().UnixNano(), rand.Int())
	handler.signal("EPHEMERA_" + handler.ephemera)

	return nil
}

func (handler *Handler) writeNode(name string) error {
	handler.nodes = append(handler.nodes, name)
	return nil
}

func (handler *Handler) lead() error {
	err := handler.push()
	if err != nil {
		return karma.Format(
			err,
			"can't push",
		)
	}

	handler.signal("PUSH")

	return nil
}

func (handler *Handler) pull() error {
	cmd := git(handler.dir, "remote", "update")
	err := cmd.Run()
	if err != nil {
		return karma.Format(err, "unable to update remote")
	}

	cmd = git(handler.dir, "merge", "--no-commit", "origin/master")
	err = cmd.Run()
	if err != nil {
		return karma.Format(err, "unable to pull repository changes")
	}

	return nil
}

func (handler *Handler) push() error {
	for {
		cmd := git(handler.dir, "add", ".")
		if err := cmd.Run(); err != nil {
			return karma.Format(err, "unable to git add")
		}

		stdout, _, err := cmd.Output()
		if err != nil {
			if !bytes.Contains(
				stdout,
				[]byte("nothing to commit, working tree clean"),
			) {
				return karma.Format(err, "unable to git commit changes")
			}
		}

		cmd = git(handler.dir, "push", "origin", "master")
		err = cmd.Run()
		if err != nil {
			if strings.Contains(err.Error(), "[rejected]") {
				err := handler.pull()
				if err != nil {
					log.Errorf(err, "unable to pull")
				}

				continue
			}

			return karma.Format(err, "unable to push changes")
		}

		break
	}

	return nil
}

func (handler *Handler) serve(cmd string, node string, data string) error {
	if strings.HasPrefix(cmd, "EPHEMERA_") {
		value := strings.TrimPrefix(cmd, "EPHEMERA_")
		if value == handler.ephemera {
			handler.node = node

			if handler.node == handler.nodes[0] {
				return handler.lead()
			}

			return nil
		}
	}

	if node == handler.node {
		return nil
	}

	switch cmd {
	case "PUSH":
		err := handler.pull()
		if err != nil {
			return karma.Format(
				err,
				"can't pull",
			)
		}

		err = handler.push()
		if err != nil {
			return karma.Format(
				err,
				"can't push",
			)
		}

		handler.signal("PULL")
		os.Exit(0)

	case "PULL":
		err := handler.pull()
		if err != nil {
			return karma.Format(
				err,
				"can't pull",
			)
		}

		os.Exit(0)
	}

	return nil
}

func (handler *Handler) signal(data string) {
	fmt.Println(handler.prefix, "SYNC", data)
}

func git(directory string, values ...string) *lexec.Execution {
	return lexec.NewExec(
		nil,
		exec.Command("git", append([]string{"-C", directory}, values...)...),
	)
}
