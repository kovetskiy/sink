package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/docopt/docopt-go"
	"github.com/kovetskiy/ko"
	"github.com/reconquest/executil-go"
	"github.com/reconquest/karma-go"
	git "gopkg.in/src-d/go-git.v4"
)

var (
	version = "[manual build]"
	usage   = "gitmon " + version + os.ExpandEnv(`


Usage:
  gitmon [options] -L
  gitmon [options]
  gitmon -h | --help
  gitmon --version

Options:
  -c --config <path>   Path to config. [default: $HOME/.guts/gitmon.conf]
  -d --dir <path>      Dir to use for saving gitmon stats. [default: $HOME/.guts/gitmon/]
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

	if args["-L"].(bool) {
		listActions(repos, args["--dir"].(string))
	} else {
		writeStates(repos, args["--dir"].(string))
	}
}

type Pull struct {
	Path    string
	Reasons []string
}

func listActions(repos []Repo, dir string) {
	hostname, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	hosts, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		log.Fatalln(err)
	}

	machines := map[string][]State{}
	for i, host := range hosts {
		host = filepath.Base(host)

		hosts[i] = host

		machines[host] = readStates(dir, host)
	}

	pulls := []Pull{}

	for _, repo := range repos {
		path := strings.Replace(repo.Path, home, "~/", 1)
		current := findState(machines[hostname], path)
		if current == nil {
			continue
		}

		var reasons []string
		for _, machine := range hosts {
			if machine == hostname {
				continue
			}

			other := findState(machines[machine], path)
			if other == nil {
				continue
			}

			if other.Commits > current.Commits {
				reasons = append(
					reasons,
					fmt.Sprintf(
						"%s: +%d commits",
						machine,
						other.Commits-current.Commits,
					),
				)
			}

			if other.Head != current.Head {
				reasons = append(
					reasons,
					fmt.Sprintf("%s: %q", machine, other.Head),
				)
			}

			if !other.Clean {
				reasons = append(
					reasons,
					fmt.Sprintf("%s: dirty", machine),
				)
			}
		}

		if len(reasons) > 0 {
			pull := Pull{
				Path:    path,
				Reasons: reasons,
			}

			pulls = append(pulls, pull)
		}
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 1, 1, ' ', 0)
	for _, pull := range pulls {
		fmt.Fprintf(writer, "%s\t%s\n", pull.Path, strings.Join(pull.Reasons, ", "))
	}
	writer.Flush()
}

func findState(states []State, path string) *State {
	for _, state := range states {
		if state.Path == path {
			return &state
		}
	}

	return nil
}

func readStates(dir string, host string) []State {
	contents, err := ioutil.ReadFile(filepath.Join(dir, host))
	if err != nil {
		log.Fatalln(err)
	}

	states := []State{}
	for _, line := range strings.Split(string(contents), "\n") {
		if line == "" {
			continue
		}

		chunks := strings.Fields(line)
		commits, err := strconv.Atoi(chunks[1])
		if err != nil {
			log.Fatalln(chunks[1], err)
		}

		var clean bool
		if chunks[4] == "clean" {
			clean = true
		}

		state := State{
			Path:    chunks[0],
			Commits: commits,
			Head:    chunks[2],
			Hash:    chunks[3],
			Clean:   clean,
		}

		states = append(states, state)
	}

	return states
}

func writeStates(repos []Repo, dir string) {
	hostname, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	file, err := os.Open(filepath.Join(dir, hostname))
	if err != nil {
		log.Fatalln(err)
	}

	defer file.Close()

	writer := tabwriter.NewWriter(file, 0, 1, 1, ' ', 0)
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

	// CommitObjects.ForEach is too slow
	stdout, _, err := executil.Run(
		exec.Command("git", "-C", target.Path, "rev-list", "--count", "HEAD"),
	)
	if err != nil {
		return nil, err
	}

	commits, err := strconv.Atoi(strings.TrimSpace(string(stdout)))
	if err != nil {
		return nil, karma.Format(
			err,
			"%s", string(stdout),
		)
	}

	// repo.Worktree is insanely slow
	stdout, _, err = executil.Run(
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
