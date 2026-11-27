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
//
// An interrupted install is. npx keeps a half-written install and reuses it
// on every later start (seen on v1.5.0: npm failed with ECOMPROMISED while
// writing restate-server, leaving its platform package without a binary, and
// the client's automatic retry ran on). A platform package that is present
// without its binary is that case: say so, name the npx directory to remove,
// and do not serve or run an engine on it.
"use strict";

const { spawn } = require("child_process");
const fs = require("fs");
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

// The npx directory holding a package, or the node_modules root otherwise.
function installRoot(pkgDir) {
  const i = pkgDir.lastIndexOf(`${path.sep}node_modules${path.sep}`);
  return i < 0 ? pkgDir : pkgDir.slice(0, i);
}

// A package that resolves but whose binary is missing or empty.
function interrupted(pkg, rel, opts) {
  let dir;
  try {
    dir = path.dirname(require.resolve(`${pkg}/package.json`, opts));
  } catch (e) {
    return undefined;
  }
  const file = path.join(dir, rel);
  try {
    if (fs.statSync(file).size > 0) return undefined;
  } catch (e) {
    // missing: interrupted
  }
  return { pkg, file, root: installRoot(dir) };
}

function interruptedMessage(broken) {
  const root = broken.root;
  const npx = root.includes(`${path.sep}_npx${path.sep}`);
  return (
    `${broken.pkg} is installed without its binary (${broken.file}): an earlier ` +
    "install was interrupted, and npm reuses it. To repair, " +
    (broken.canStop ? "stop the engine (npx -y @turfbuild/turf-mcp-server engine stop), " : "") +
    "turn the server off in your agent, " +
    (npx ? `remove ${root} (rm -rf '${root}'), ` : "reinstall @turfbuild/turf-mcp-server, ") +
    "then run `npx -y @turfbuild/turf-mcp-server engine status` in a terminal and let " +
    "the download finish before turning the server back on."
  );
}

const ownBroken = interrupted(`@turfbuild/turf-mcp-server-${key}`, "bin/turf-mcp-server");
if (ownBroken) fail(interruptedMessage(ownBroken));

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
  let restateBroken;
  try {
    const top = path.dirname(require.resolve("@restatedev/restate-server/package.json"));
    restateBroken = interrupted(`@restatedev/restate-server-${key}`, "bin/restate-server", {
      paths: [top],
    });
  } catch (e) {
    restateBroken = undefined;
  }
  if (restateBroken) {
    // Serving MCP or running an engine would fail later and less clearly, or
    // run another restate-server than the one this package pins. Other
    // commands (engine stop/status, license, --version) need no restate-server
    // and are part of the repair, so they run with the warning.
    const args = process.argv.slice(2);
    const serving =
      args.length === 0 ||
      (args[0].startsWith("-") && !["--version", "--help", "-h"].some((a) => args.includes(a)));
    const engineRun = args[0] === "engine" && args[1] === "run";
    restateBroken.canStop = true;
    if (serving || engineRun) fail(interruptedMessage(restateBroken));
    process.stderr.write(`turf-mcp-server: warning: ${interruptedMessage(restateBroken)}\n`);
  } else {
    const restate = restateServer();
    if (restate) env.TURF_RESTATE_SERVER = restate;
  }
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
