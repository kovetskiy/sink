package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"text/tabwriter"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/docopt/docopt-go"
	"github.com/kovetskiy/ko"
	"github.com/reconquest/executil-go"
	git "gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
)

var (
	version = "[manual build]"
	usage   = "gitmon " + version + os.ExpandEnv(`


Usage:
  gitmon [options]
  gitmon -h | --help
  gitmon --version

Options:
  -c --config <path>   Path to config. [default: $HOME/.guts/gitmon.conf]
  --cpuprofile <path>  Profile cpu.
  -h --help            Show this screen.
  --version            Show version.
`)
)

type Repo struct {
	Path string
}

type Config struct {
	Git []Repo
}

type State struct {
	Path    string
	Head    string
	Hash    string
	Commits int
	Clean   bool
}

var home = strings.TrimRight(os.Getenv("HOME"), "/") + "/"

func main() {
	args, err := docopt.Parse(usage, nil, true, version, false)
	if err != nil {
		panic(err)
	}

	if path, ok := args["--cpuprofile"].(string); ok {
		file, err := os.Create(path)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(file)
		go func() {
			time.Sleep(time.Second * 10)
			pprof.StopCPUProfile()
		}()

	}

	var config Config
	err = ko.Load(args["--config"].(string), &config, yaml.Unmarshal)
	if err != nil {
		log.Fatalln(err)
	}

	repos := []Repo{}
	for _, repo := range config.Git {
		path := strings.Replace(repo.Path, "~/", home, 1)

		matches, err := filepath.Glob(path)
		if err != nil {
			log.Fatalln("bad pattern:", path, err)
		}

		for _, match := range matches {
			repos = append(repos, Repo{Path: match})
		}
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 1, 1, ' ', 0)
	for _, repo := range repos {
		state, err := getState(repo)
		if err != nil {
			log.Fatalln(err)
		}

		if state == nil {
			continue
		}

		msg := "clean"
		if !state.Clean {
			msg = "dirty"
		}

		fmt.Fprintf(writer, "%s\t%d\t%s\t%s\t%s\n", state.Path, state.Commits, state.Head, state.Hash, msg)
	}
	writer.Flush()
}

func getState(target Repo) (*State, error) {
	repo, err := git.PlainOpen(target.Path)
	if err != nil {
		if err == git.ErrRepositoryNotExists {
			return nil, nil
		}
		return nil, err
	}

	head, err := repo.Head()
	if err != nil {
		return nil, err
	}

	commitObjects, err := repo.CommitObjects()
	if err != nil {
		return nil, err
	}

	commits := 0
	commitObjects.ForEach(func(_ *object.Commit) error {
		commits++
		return nil
	})

	// repo.Worktree is insanely slow
	stdout, _, err := executil.Run(
		exec.Command("git", "-C", target.Path, "status", "--short"),
	)
	if err != nil {
		return nil, err
	}

	clean := len(strings.TrimSpace(string(stdout))) == 0

	state := &State{
		Path:    strings.Replace(target.Path, home, "~/", 1),
		Head:    head.Name().Short(),
		Hash:    head.Hash().String(),
		Commits: commits,
		Clean:   clean,
	}

	return state, nil
}
