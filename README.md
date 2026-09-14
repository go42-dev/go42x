<div align="center"><pre>
░██████╗░░█████╗░░░██╗██╗░█████═╗░██╗░░██╗░
██╔════╝░██╔══██╗░██╔╝╚█║█════██║░╚██╗██╔╝░
██║░░██╗░██║░░██║██╔╝░░╚╝░░███╔═╝░░╚███╔╝░░
██║░░╚██╗██║░░██║███████╗██╔══╝░░░░██╔██╗░░
╚██████╔╝╚█████╔╝╚════██║███████║░██╔╝╚██╗░
░╚═════╝░░╚════╝░░░░░░╚═╝╚══════╝░╚═╝░░╚═╝░
</pre></div>
<p align="center">
<a href="https://opensource.org/licenses/MIT"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="licence"></a>
<a href="https://golang.org/"><img src="https://img.shields.io/badge/Go-1.27-00ADD8?style=flat&logo=go" alt="goversion"></a>
<a href="https://github.com/go42-dev/go42x/releases"><img src="https://img.shields.io/github/v/release/go42-dev/go42x" alt="release"></a>
</p>

# go42x

Helper tool for go42 project.

## Installation

### Homebrew

```bash
brew tap go42-dev/go42x
brew install go42x
```

### Go

```bash
go install github.com/go42-dev/go42x@latest
```

### Download Binary

Download the latest binary from the [releases page](https://github.com/go42-dev/go42x/releases).

## Embedded assets

Bundled templates and the configuration schema live in [`assets/agentenv/`](assets/agentenv/).
Edit these files to change the defaults bundled into the CLI; [`assets/embed.go`](assets/embed.go) embeds them at build time.
