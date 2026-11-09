#!/usr/bin/env node
// Runs the flow binary for this platform.
"use strict";

const { spawn } = require("node:child_process");
const { existsSync, realpathSync } = require("node:fs");
const path = require("node:path");

const { name } = require("../package.json");
const root = realpathSync(path.join(__dirname, ".."));

// Errors read as the install script's, with "error:" in red on a terminal.
function fail(message) {
  const color = process.stderr.isTTY && !process.env.NO_COLOR && process.env.TERM !== "dumb";
  console.error(`${color ? "\x1b[31merror:\x1b[39m" : "error:"} flow: ${message}`);
  process.exit(1);
}

// The package holds a binary for every platform, next to this file.
const bin = path.join(__dirname, `flow-${process.platform}-${process.arch}`);
if (!existsSync(bin)) {
  fail(`unsupported platform: ${process.platform}/${process.arch}`);
}

// Tells flow whether pnpm or npm installed it, so it updates with the same one.
// pnpm marks its node_modules with .modules.yaml.
function managedBy() {
  for (let dir = root; dir !== path.dirname(dir); dir = path.dirname(dir)) {
    const modules = path.join(dir, "node_modules");
    try {
      if (existsSync(path.join(modules, ".modules.yaml")) && realpathSync(path.join(modules, name)) === root) {
        return "pnpm";
      }
    } catch {}
  }
  return "npm";
}

const child = spawn(bin, process.argv.slice(2), {
  stdio: "inherit",
  env: { ...process.env, FLOW_MANAGED_BY: managedBy() },
});
child.on("error", (err) => fail(`launch failed: ${err.message}`));

// Pass stop signals on to flow.
const signals = ["SIGINT", "SIGTERM", "SIGHUP"];
for (const signal of signals) {
  process.on(signal, () => child.kill(signal));
}

// Exit the way flow exited.
child.on("exit", (code, signal) => {
  if (signal) {
    for (const s of signals) {
      process.removeAllListeners(s);
    }
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 1);
});
