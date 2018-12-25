# sink

Two way synchornization tool with git and inotify.

Nothing extra-ordinary actually, **sink** just creates a commit, pulls and
pushes when gets any filesystem event.

## Background

I needed to sync .zsh_history file between two pc: desktop and laptop. Yes, I
have dotfiles and that's not really appropriate in this case because
.zsh_history contains sensitive information that I don't want to share with
open source world.

So, with **sink** you just do following:

1. create a directory (can be specified using `-d --dir <path>` flag) like `~/.guts/`
2. move your file to ~/.guts/ directory
3. create symlink like `ln -s ~/.guts/.zsh_history ~/.zsh_history`
4. run `sink`
5. type on one pc and automatically get updates on another

In order to avoid unstoppable synchornization sink has interval between syncs,
by default it's 30 seconds.

## Installation

```
go get github.com/kovetskiy/sink
```

Also, you can find the package in Arch Linux User Repository.

## Options

- `-d --dir <path>` - Path of guts to sync. `[default: $HOME/.guts/]`
- `-i --interval <path>` - Interval between syncs in seconds. `[default: 30]`
- `--trace` - Enable trace messages.
- `-h --help` - Show this screen.
- `--version` - Show version.

## License

MIT.
