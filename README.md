<p align="center">
  <a href="https://github.com/unaquatic/flow">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="docs/logo-dark.svg">
      <img src="docs/logo-light.svg" alt="flow">
    </picture>
  </a>
</p>

<p align="center">
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.27-555?labelColor=888&logo=go&logoColor=white&style=flat-square" alt="Go"></a>
  <a href="https://github.com/unaquatic/flow/blob/main/LICENSE"><img src="https://img.shields.io/github/license/unaquatic/flow?labelColor=888&color=555&style=flat-square" alt="License"></a>
  <a href="https://github.com/unaquatic/flow/stargazers"><img src="https://img.shields.io/github/stars/unaquatic/flow?labelColor=888&color=555&style=flat-square" alt="GitHub stars"></a>
</p>

<p align="center"><b>A tiny and fast agent written in golang.</b></p>

---


## Installation

```sh
curl -fsSL https://aquatic.dev/flow/install | sh   # macOS, Linux, Git Bash on Windows
npm install -g @unaquatic/flow
pnpm add -g @unaquatic/flow
```

The install script downloads flow from its GitHub release, checked against
the digest GitHub reports for it, puts it in `~/.flow/bin`, links it from
`~/.local/bin`, and adds that to `PATH`. `FLOW_VERSION`, `FLOW_INSTALL_DIR` and
`FLOW_NO_MODIFY_PATH=1` change what it does.

On Windows, flow runs in Windows Terminal or any Windows 10+ console. Its bash
tool runs commands with Git Bash, so install
[Git for Windows](https://git-scm.com/download/win), or put another
`bash.exe` on `PATH`.

## Updating

When it starts, flow checks for a newer release where it was installed from:
GitHub for the install script, npm for npm and pnpm. It installs the release
in the background the same way, and runs it from the next start, which shows
`Updated to v1.2.3` under the status. Install output goes to
`~/.flow/logs/update.log`. Windows cannot replace a running flow, so there
flow updates only when you run `flow update`.

```sh
flow update          # install the latest release now
flow update --check  # only check for one
```

Set `"auto_update": false` in `~/.flow/config.json`, or
`FLOW_DISABLE_AUTOUPDATE=1`, to turn off the background check and install.

## Development

Build and run from source with Go 1.27+.

```sh
go build ./cmd/flow/
./flow
```

## License

Flow is released under the [MIT License](LICENSE).
