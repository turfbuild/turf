#!/usr/bin/env node
// The npm launcher of turf-mcp-server: finds this platform's binary in its
// optional dependency @turfbuild/turf-mcp-server-<os>-<arch> and runs it with
// this process's arguments and stdio.
//
// It is an MCP stdio server's launcher, so it never writes to stdout: what it
// has to say goes to stderr, and stdout is the child's alone.
//
// restate-server comes from Restate's own package, @restatedev/restate-server,
// a dependency of this one. Its real binary sits in Restate's platform package,
// which is resolved from where Restate's package is installed (nested or
// hoisted alike) and handed to the server as TURF_RESTATE_SERVER, unless that is
// already set. Not finding it is no failure: the server needs restate-server
// only to run a local engine.
"use strict";

const { spawn } = require("child_process");
const os = require("os");
const path = require("path");

const SUPPORTED = ["darwin-arm64", "darwin-x64", "linux-arm64", "linux-x64"];
const key = `${process.platform}-${process.arch}`;

function fail(msg) {
  process.stderr.write(`turf-mcp-server: ${msg}\n`);
  process.exit(1);
}

if (!SUPPORTED.includes(key)) {
  fail(`no build for ${key} (supported: ${SUPPORTED.join(", ")})`);
}

let bin;
try {
  bin = require.resolve(`@turfbuild/turf-mcp-server-${key}/bin/turf-mcp-server`);
} catch (e) {
  fail(
    `the platform package @turfbuild/turf-mcp-server-${key} is not installed ` +
      "(an install with optional dependencies omitted skips it); reinstall @turfbuild/turf-mcp-server",
  );
}

function restateServer() {
  try {
    const top = path.dirname(require.resolve("@restatedev/restate-server/package.json"));
    return require.resolve(`@restatedev/restate-server-${key}/bin/restate-server`, { paths: [top] });
  } catch (e) {
    return undefined;
  }
}

const env = { ...process.env };
if (!env.TURF_RESTATE_SERVER) {
  const restate = restateServer();
  if (restate) env.TURF_RESTATE_SERVER = restate;
}

const child = spawn(bin, process.argv.slice(2), { stdio: "inherit", env });

const FORWARDED = ["SIGINT", "SIGTERM", "SIGHUP", "SIGUSR1", "SIGUSR2"];
const forward = (signal) => {
  if (child.exitCode === null && child.signalCode === null) child.kill(signal);
};
for (const signal of FORWARDED) process.on(signal, forward);

child.on("error", (err) => fail(`cannot run ${bin}: ${err.message}`));

child.on("exit", (code, signal) => {
  if (signal) {
    // Die of the same signal, so a parent sees what the server saw.
    for (const s of FORWARDED) process.removeListener(s, forward);
    process.kill(process.pid, signal);
    // A signal whose default action is not to terminate (or that Node keeps
    // for itself, SIGUSR1) leaves us here: exit as a shell would report it.
    setTimeout(() => process.exit(128 + (os.constants.signals[signal] || 0)), 100).unref();
    return;
  }
  process.exit(code === null ? 1 : code);
});
