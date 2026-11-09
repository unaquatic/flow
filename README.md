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
npm install -g @unaquatic/flow
```

## Updating

Once a day, flow checks npm for a newer release when it starts, installs it in
the background with the package manager that installed flow (npm or pnpm), and
runs it from the next start, which shows `Updated to v1.2.3` under the status.
Install output goes to `~/.flow/logs/update.log`.

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
