# Marina

A live dashboard for every local server you have running. It finds them by
itself, tells you which project each one belongs to, and gets you into them with
one click.

```
● marina — 33 boats docked · http://127.0.0.1:7777
  4 apps · 13 services · 9 infra · 7 system · 21 speak HTTP · swept in 150ms

  APPS
    :3000   media-tool → frontend       Vite    up 1d 17h
    :5001     └ server.js       backend  Node    up 23h 42m
  ★ :5173   Webapp UI → frontend        Vite    up 1d 22h
    :3001     └ index.ts        backend  tsx     up 1d 22h
    :3002     └ apiServer.js    backend  tsx     up 1d 22h
    :3004     └ queueWorker.js  backend  tsx     up 1d 22h
```

Marina runs as a login-time daemon plus a menu bar agent, so it is simply always
there. Nothing to start, nothing to remember.

## Install

On a Mac that has never built this before:

```bash
git clone <this repo> ~/projects/marina
cd ~/projects/marina
npm run setup
```

`setup` checks what the machine is missing, tells you what it wants to install
and why, asks once, and then builds and installs. On a machine that already has
the toolchain it goes straight through to the install. To see what it would do
without changing anything:

```bash
npm run doctor
```

Once set up, `npm start` is all you need — it rebuilds and upgrades in place, and
is safe to re-run after every `git pull`.

Everything lands in `~/.local/share/marina`, with two launchd agents so the
daemon and the menu bar icon come back after a reboot. Nothing is written outside
your home directory.

### What it needs

| | why | if missing |
| --- | --- | --- |
| macOS 13+ | `lsof`, `launchd`, and the menu bar agent | hard requirement |
| Xcode Command Line Tools | Swift, for the menu bar app | `setup` opens Apple's installer |
| Go | builds the daemon | `setup` installs via Homebrew |
| Node | builds the dashboard | `setup` installs via Homebrew |
| Postgres | pins, nicknames, and history | **optional** — Marina runs fine without it |

Postgres is genuinely optional. The daemon connects lazily and mirrors everything
in memory, so it serves live state at login whether or not Postgres is up, and
creates its own `marina` database on first connect. Skip it with
`npm run setup -- --no-postgres`; you lose only the things that must outlive a
restart.

Arguments pass through to the installer, so a second machine that keeps projects
somewhere else can say:

```bash
npm run setup -- --roots ~/projects,~/work
```

### Moving it to another Mac

There is no state to migrate and nothing machine-specific in the build. Clone,
run `npm run setup`, and the new machine discovers its own servers. Pins,
nicknames, and port history live in that machine's Postgres and are deliberately
local — a pin on your laptop should not follow you to your desktop, where a
different set of things is running.

Uninstall completely with `npm run uninstall`: it boots out both launchd agents,
deletes their plists, `~/.local/share/marina`, and the `marina` symlink, and
leaves your projects and the database alone. Add `-- --drop-db` if you also want
the `marina` database gone.

## Using it

- **Dashboard** — <http://127.0.0.1:7777>, or `marina open`
- **Menu bar** — the waves icon at the top right, showing a live count. The
  dropdown lists your apps; click one to open it.
- **Terminal** — `marina status`, `marina ashore`, `marina start <name>`

In the dashboard, clicking anywhere on a row opens that app. `⌘K` or `/` jumps to
search; type a port number and press Enter to go straight there. Hovering a row
reveals pin, rename, and copy-address.

## Two views

The switch in the header picks how to read the same data. Your choice is
remembered.

**Manifest** is the working view: a dense list anchored on port numbers, grouped
by project, built for scanning thirty rows and getting into one.

**Harbour** is the same servers as a place. It is deliberately literal, and every
element of the scene is a fact rather than a decoration:

| In the harbour | Means |
| --- | --- |
| Out on the water, line cast | The port answers HTTP — it's serving |
| Moored at the pier | Listening, but not answering HTTP yet (often still compiling) |
| A wake behind the hull | Started in the last 20 seconds |
| Sail colour | Its project — one fleet, one colour |
| Number on the hull | Its port |
| Pennant on the mast | Pinned |
| Floats on a boat's net | The services behind that app |
| On a cradle in the boatyard | A project that isn't running — click to launch it |
| Buildings on the shore | Infra: Postgres, Redis, Mongo, Jaeger. One building per service, `+n` for its extra ports |
| Buoys | The machine's own port-holders — Control Centre, rapportd |

Amber is reserved: it only ever means "just started" or "pinned", so no fleet is
ever assigned it. Boats are clickable exactly when they're out fishing, because
that is the same question as "does this port serve HTTP". With
`prefers-reduced-motion` the harbour becomes a still illustration and loses
nothing — no meaning is carried by movement alone.

## What it actually knows

A port number on its own is not useful when you have thirty of them. Marina
resolves each listening socket into something you recognise:

| It reads | To tell you |
| --- | --- |
| the process's working directory | which repo and which package |
| the git root above it | the project name, and the path within a monorepo |
| the command line | the framework — Vite, Next.js, FastAPI, Rails, tsx… |
| the launched script | which of thirteen sibling workers this one is |
| an HTTP probe | whether it's a web app worth clicking, and its page title |
| `ps` start time | how long it's been up |

That last-but-one point is the difference between a list of ports and a useful
dashboard. Thirteen workers started from one package all look identical until you
surface the entry script:

```
:3004   queueWorker.js    :3008   statsWorker.js
:3005   imageWorker.js    :3009   feedWorker.js
```

Services that aren't yours are separated out rather than hidden: Postgres, Redis,
and Mongo land under **Infra**, and macOS's own port-holders under **System**.

## Apps versus services

A count of listening ports badly overstates how much you are running. One project
can be a single UI plus a dozen workers that only exist to serve it — thirteen
ports, but one thing you would ever open.

So Marina works out each project's **front door** and treats the rest as its
services. The evidence is already in the data: a UI answers with an HTML document
that has a `<title>` and runs a UI framework, while workers answer with JSON.
Nothing is guessed — a project whose members are all equals stays flat rather
than having a hierarchy invented for it.

The result, on this machine: **17 app ports become 4 apps and 13 services.**

```
★ :5173   Webapp UI → frontend    Vite    up 1d 22h
  :3001     └ index.ts            tsx     up 1d 22h
  :3002     └ apiServer.js        tsx     up 1d 22h
  :3004     └ queueWorker.js      tsx     up 1d 22h
  …
```

In the **manifest**, services fold under their app, collapsed by default, with a
disclosure that says how many and over which ports. In the **harbour**, the app
is a boat and its services are floats on its net. Every port stays individually
clickable in both.

Pins are deliberately kept separate from this. A role says what a service *is*
within its project; a pin says what you want kept close. Pinning a worker you are
debugging must not demote the real UI to being a service of that worker — so
pinning any part of a project surfaces the whole cluster, and changes no roles.

## What's *not* running

Marina also scans your project directories for apps that could be running but
aren't — `~/projects` by default.

### Which directories get scanned

Most machines keep projects in more than one place. Open **scanned directories**
under the boatyard to see the current list with a project count each, add one, or
remove one. Changes take effect immediately — no restart, and no waiting for the
scan interval — and are saved in `~/.local/share/marina/roots.json` so they
survive a reboot.

You can also set them at install time, which is the easier path when setting up a
new machine:

```bash
npm run setup -- --roots ~/projects,~/git,~/workspace
```

Three things are worth knowing, because each one otherwise fails quietly:

- **Each directory is read one level deep.** `~/git/app` is found; `~/git/clients/app`
  is not. Add the subfolder itself for those. A root showing **0 projects** in the
  list is usually this.
- **A missing or unreadable directory is skipped in silence.** The list marks those
  cases explicitly rather than leaving a renamed folder looking fine.
- **Listing every directory also improves naming, not just the boatyard.** These
  roots double as *boundaries* when identifying what's already running: a directory
  that holds projects is never itself a project. That's the rule that stops a
  monorepo package from calling itself `frontend`. If projects live in `~/git` and
  Marina doesn't know that, the upward walk loses that boundary and a repo-less
  monorepo in there can pick the wrong name. `$HOME` and `/` are always boundaries,
  so nothing can ever be named after your home directory.

Precedence is deliberately blunt: **`roots.json` wins if it exists**, and
`--roots`/`MARINA_ROOTS` only seed a machine that has never set them. Passing
`--roots` explicitly clears the saved list and says so, so the flag always works
when you reach for it.

It works out how each one starts by reading the directory: the `dev` script from
`package.json` (with the package manager taken from the lockfile, because running
a pnpm workspace with npm doesn't work), `cargo run`, `manage.py runserver`,
`go run .`, a `dev` target in a Makefile, or a lone Python entry point. A project
with no recognisable start command is **counted but not listed**, and the count is
shown — the folder never looks emptier than it is.

On this machine that finds 21 startable projects and reports 8 it can't start.

In the manifest they appear under **Ashore**, each with the exact command. In the
harbour they're boats **in the boatyard**, hauled out on cradles with no hull
number, because they have no port yet.

```bash
marina ashore              # list what could be started
marina start app-two      # start it (unique prefixes work)
marina stop app-two       # and stop it again
```

### Which port will it take?

With `:3000` and `:5173` shared across a dozen projects, a collision is the most
likely reason a launch misbehaves. So each project ashore shows the port it is
expected to bind, and warns when something already holds it — amber, with `⚠`, and
a second click required to start anyway.

The evidence is ranked, and how it was obtained is always available on hover,
because a conflict against an observed port means much more than one against a
framework's default:

| Source | How it's found | Example here |
| --- | --- | --- |
| **history** | Marina watched that project use that port | `audio-app :8931`, `app-two :4444` |
| **config** | any `.env*` or `*.config.*` file in the project | `app-three :3000`, `solo-app :3001` |
| **script** | a `--port`/`PORT=` in the start command, or in a script it runs | `profiler-ui :8000` |
| **default** | the framework's usual port — a guess, and rendered dimmer | `alerts-app :3000` |

Discovery is by pattern, not by a list of filenames: config files are globbed
(`*.config.ts`, `.env.staging`, anything), and a command like `bash scripts/dev.sh`
is followed one level down, because that is where several of these projects
actually set their ports. A brand-new config filename is read without Marina
needing to know it exists.

Two details that keep it honest: `DATABASE_PORT` and `REDIS_PORT` describe
dependencies, not this app, so they are ignored while `PORT`, `VITE_PORT`, and
`APP_PORT` are used; and a framework default is only consulted when the project
itself said nothing.

History is the part that improves on its own — it is observation, not inference,
and it comes from the same sightings table that backs uptime.

Clicking `start`, or running the command, launches it and the boat slides into
the water within a couple of seconds via the normal detection path.

### How launching is kept narrow

Starting processes deserves care, so this is deliberately restricted:

- A request names a **path, never a command**. The daemon looks that path up in
  the catalogue and runs only the command it derived itself. An unknown path is
  refused, so the endpoint cannot be used to run arbitrary things.
- The command is always **shown before you can run it** — on the button's tooltip
  and in the row itself. A button that starts a process should say what it starts.
- Output goes to `~/.local/share/marina/launches/<name>.log`, because a detached
  process with nowhere to write is one you can't debug.

Nothing is ever started automatically.

### The environment a launch gets

This is the part that is easy to get wrong, and Marina got it wrong first.

launchd starts the daemon with a minimal `PATH`
(`/usr/local/bin:/usr/bin:/bin:…`). The tools these projects need are not there —
`pnpm` and `node` come from nvm under `~/.nvm/versions/node/<version>/bin`, which
is put on `PATH` by `~/.zshrc`. So `pnpm run dev` failed with
`pnpm: command not found`, and the only place that said so was the log file.

Marina now asks your own shell what its environment is — `$SHELL -ilc`, cached for
ten minutes — and launches with that. Interactive (`-i`) matters: nvm's setup lives
in `.zshrc`, which a non-interactive shell never reads. The resolved `PATH` is
written into each log's header, so a "command not found" is self-explanatory. If
the shell can't be read, it falls back to the daemon's `PATH` plus the usual
toolchain directories rather than refusing to try.

### When a launch fails

A launch that dies is reported, with the reason, next to the project:

```
✗ webapp   sh: pnpm: command not found — that tool is not on the PATH Marina used   [view log]
```

The launcher watches the process it started. An exit inside 20 seconds means it
never got going, and the log is read for a recognisable cause — a missing command,
`EADDRINUSE`, a missing script. The failure stays visible until you launch that
project again, and `view log` opens its terminal. Earlier this showed `starting…`
forever while the log already had the answer, which is the worst of both.

### Stopping

Any running app can be stopped — the `⏻` button on its row, on its terminal card,
or `marina stop <name>`. Stopping puts it back in **Ashore**, so start and stop are
the same two clicks in the same place.

```bash
marina stop webapp
○ stopped Webapp UI — 13 processes ended (2 needed SIGKILL)
```

This is the only destructive thing Marina does, so it is fenced in:

- **Apps only.** Infrastructure and system processes are refused with a reason:
  Postgres is shared, and Marina depends on it. Marina refuses to stop itself too.
- **Addressed by port or project**, never by a PID from the client, so a stale or
  hostile request cannot pick an arbitrary process.
- **SIGTERM, then SIGKILL only if ignored** after 8 seconds — what Ctrl+C does.
- **The process group, when that group is provably just this app.** Signalling a
  group is what Ctrl+C does, and job control normally puts a dev server in a group
  of its own — which is exactly why Ctrl+C stops the server and not your shell.
  Marina verifies that rather than assuming it: before signalling a group it did
  not create, every process in the group must be working inside that project's
  directory. If anything else is in there, it falls back to individual processes.

  This matters because an app is a tree. mono-app, started from a terminal, was
  twelve processes — `npm run dev` → `concurrently` → backend and frontend chains —
  all in one group. Killing only the listener would have left the supervisor to
  restart it. Measured after a group stop: all twelve gone, and the three live
  `zsh` sessions untouched.
- Buttons **arm on first click** ("stop all 13?"). No modal for something this
  reversible, but not a hair trigger next to "copy" either.

A deliberate stop is recorded as such, so it is never reported as a crash.

A finished terminal can be cleared with **dismiss**, which deletes that log file —
only for logs Marina wrote, and only once the app has stopped. A log belonging to
someone else's process is not Marina's to delete.

Launch records are persisted to `launches.json` and re-adopted on startup, because
launched apps outlive the daemon — without that, upgrading Marina left a running
fleet it no longer recognised as its own and could not stop as a group.

### Scheduling priority: do not run dev servers as Background

Worth writing down, because it was measurable and badly wrong.

The daemon's plist originally set `ProcessType = Background`. That applies a QoS
class which **every descendant inherits**, and Background is the lowest scheduling
band — the one Spotlight indexing runs in. Apps launched from Marina inherited it:

| Started by | nice | PRI |
| --- | --- | --- |
| Marina, `ProcessType Background` | 0 | **4** |
| Marina, `ProcessType Interactive` | 0 | **31–37** |
| A terminal, for comparison | 5 | 31 |

At PRI 4 a Vite dev server plus twelve workers was effectively unresponsive, with
multi-second event-loop stalls that consumed almost no CPU — the app's own
instrumentation reported `wall=2707ms cpu=444ms`. It also lowered their jetsam
priority, making them likelier to be killed under memory pressure. After the
change, the same fleet shows no stalls at all and the UI answers in under 500ms.

Marina's own cost is ~0.5% of one core, so there was never anything to gain by
throttling it.

### Surviving an upgrade

Launched apps are `setsid`'d into their own session, but that alone does not save
them from `launchctl bootout` — which is what upgrading does — because launchd
kills a job's descendants. The daemon's plist therefore sets
`AbandonProcessGroup`, which tells launchd to leave them alone. Verify with:

```bash
launchctl print gui/$(id -u)/tech.bocchino.marina | grep -i abandon
```

## Terminals

`/logs` — the third tab — shows every terminal Marina can reach, as a grid of live
previews. Click one for a full-height view that follows its tail. Colours survive:
Vite's red error output is red here too. `copy` gives you the text with escape
codes stripped.

Marina can show a terminal in exactly two cases, and it says which:

| Badge | Means |
| --- | --- |
| `launched` | Marina started it, so it owns the pipe and wrote the log itself |
| `file` | Started elsewhere, but its output goes to a regular file Marina can read |

Everything else is listed under **running, output not reachable**, with the actual
reason — because "no logs" is not an explanation. Marina reads each app's stdout
descriptor to find out:

- **a pipe held by the terminal that started it** — the common case. A pipe is
  point-to-point and your terminal emulator is already the reader; there is
  genuinely nothing left for a third process to see.
- **straight to `/dev/ttysNNN`** — it names the terminal, so you know where to look.
- **discarded** (`/dev/null`), or **a file that has since been deleted**.

To bring one of those into this view, either start it from Marina, or redirect its
output to a file when you start it:

```bash
npm run dev > dev.log 2>&1     # readable — Marina will show it
npm run dev | tee dev.log      # still a pipe, still unreachable
```

There is no way to attach to a running process's stdout after the fact without
root and disabled SIP, so this is the honest limit rather than a missing feature.

Marina's own daemon log appears here too, which is the quickest way to see what it
is doing. Logs are plain files under `~/.local/share/marina/launches/`, so
`tail -f` works just as well. Each launch truncates its own log; the view notices
and starts over.

## Health

The **Health** tab shows what each running app is costing the machine, ordered by
CPU, with a trace of the last few minutes and `logs` / `stop` beside each one. In
the harbour, every boat carries a small load meter — and rides a couple of pixels
lower in the water the harder it is working.

Three decisions make the numbers trustworthy rather than decorative:

**CPU is differenced, not read.** macOS's `ps %CPU` column is a decayed average
over an unspecified window. Marina samples cumulative CPU time and divides by the
real interval, so "14% of one core over the last 3 seconds" means exactly that.

**Cost is attributed by process group, not by process tree.** A dev server's cost
lives in its children, so a single process badly understates it — but a tree walk
overstates it, because anything Marina launched is a *descendant of Marina*. The
first version credited Marina with webapp's 13 processes and 2.4GB, counting it
twice. A process group is what job control calls one job, so grouping by it gives
Webapp its 19 processes and leaves Marina with its own 1.

**The scale is cores, not a share of the machine.** One core fully busy is ordinary
for a dev server mid-rebuild; three cores pinned is what actually makes a laptop
lag. Traces scale to each app's own peak, so a worker that varies between 6% and
19% shows that variation instead of being flattened against a 400% axis.

### Keeping it free

The sampling itself is one `ps` call covering every process — 40ms for 800 of them,
and the whole daemon measures at **0.67% of one core and 19MB** with health running.

The subtler cost was avoided by design. CPU changes on every sample, so putting it
in the state that drives Server-Sent Events would mean broadcasting continuously
and destroying the property that a quiet machine produces no traffic at all.
Metrics therefore live on a separate polled endpoint, the UI never polls faster
than the daemon samples, and it stops asking entirely when the page is hidden.

Verified: with health running, an idle 24 seconds on the event stream produces
exactly one frame — the initial snapshot.

### The page's own cost

The daemon was never the expensive half. A review of the browser side found the
dashboard costing **~24% of a CPU core while sitting still**, which is worse than
everything it was measuring.

Three separate causes, each measured rather than guessed at, all in the harbour's
scenery:

**A transform animation on an SVG child relayouts the page every frame.** Blink
cannot composite a transform on an element *inside* `<svg>`, so the bobbing float
on each boat forced a full style-and-layout pass 60 times a second — confirmed at
**60 layouts/s, against 7 with that one animation disabled**. `will-change` does
not help; nothing promotes an SVG child to its own layer. The float is now a DOM
element positioned over the boat, and the buoy animates its `<svg>` root rather
than a `<circle>` inside it. Layout dropped to ~10/s.

**`background-position` cannot be composited either.** The drifting water ran on
the main thread, repainting every frame. It now translates the strip by exactly
one gradient period inside an `overflow:hidden` clip, which loops seamlessly and
runs on the compositor.

**Any perpetual animation keeps the whole browser out of idle.** This was the
big one, and it is not about how many elements move. A page with **one** running
animation cost 21% of a core; the same page with none cost 6%. The status dot in
the top bar pulsed while *connected* — on every route, including the plain table
— so the table view alone burned ~24% of a core to say "fine". Motion now marks
only the states you can act on: the dot is steady when connected and pulses when
it is not, and the terminals list shows "running" as a lit dot rather than a
pulsing one.

The harbour is *meant* to move, so its ambient motion is kept and paused instead
when nobody is looking — see `web/src/lib/calm.ts`. Chrome already stops
animating a hidden tab but not a visible unfocused one, which is exactly how a
dashboard gets left on a second monitor.

| | before | after |
| --- | --- | --- |
| manifest table (default view) | 24.0% | **5.3%** |
| harbour, focused | 26.2% | 26.1% |
| harbour, window unfocused | 26.2% | **4.3%** |
| harbour, tab hidden | 0.3% | 0.3% |

Share of one core, 60s samples, CPU attributed to the test browser's own process
tree. The harbour costs what it always did *while you are watching it* — that is
the animated scene, and it is now only paid for while someone is looking at it.

Getting the focused row honestly took some care: the window manager kept stealing
focus mid-sample, which silently paused the scene and produced a flattering 14.7%.
The number above was taken with focus forced at the renderer and all 15 animations
confirmed still running at both ends of the window.

Two things this cost me, worth recording: the first CPU figures I took summed
every process matching "Google Chrome" and so included the browser I was not
testing, and a later run read all zeros because a previous script had left the tab
frozen. Both were caught by sanity checks — an impossible reading (158% CPU with
zero layouts) and a liveness assertion in the harness.

## Does Marina disturb the apps it watches?

Worth being precise about, since a dashboard that destabilises what it observes
would be worthless.

**Measured:** a server watched by Marina receives **two requests every five
minutes** — one `GET /` to read its title, and one `GET /favicon.ico` for its icon
— and that is with the dashboard open in a browser. Nothing else.

**Marina never signals any process.** The entire codebase contains three external
commands: `lsof` and `ps` (read-only), `/usr/bin/open` (your browser), and
`/bin/sh -lc` (only when you explicitly start something). There is no `Kill`, no
`Signal`, no `pkill` anywhere.

Steady state is ~0.5% of one core and 17MB, and a sweep is one `lsof` call.

The one honest caveat: if an app throws an unhandled exception on an unexpected
`GET /`, Node exits on it. That is at most one death per five minutes and cannot
take down a process tree — but you can remove even that:

```bash
bash scripts/install.sh --no-probe 3001-3013
```

Excluded ports are never contacted at all. They still appear, still show uptime,
and are marked `not probed`; you lose only the page title and the Open button.

## Reaching it from another device

The header carries the address other machines on your network use, because a dev
server you started here is often something you want to open on a phone, and the
router changes that address whenever the lease renews. Click it to copy.

Beside it is a short Bonjour name Marina publishes for this machine — `marina.local`
by default — so the URL is the same everywhere and only the port varies:

```
http://marina.local:3000
```

It is an *additional* name; the Mac keeps its own for AirDrop and Screen Sharing.
Choose another with `--mdns-name`, or publish nothing with `--mdns-name ""`. Two
machines cannot publish the same name on one network, so give the second one a
different one. The name is withdrawn when the daemon stops, so it never points at
a machine that has gone.

Two things worth knowing, because both fail quietly:

- **A server bound to loopback will not answer**, however right the address is.
  Vite and Astro bind localhost by default and need `--host`. The address hint
  says how many of your running apps actually listen on all interfaces.
- **Vite blocks unfamiliar host names.** Raw IPv4 is always allowed, but a
  `.local` name returns "Blocked request" until you allow it. One entry covers
  every Bonjour name, this Mac's and any alias:

  ```js
  // vite.config.js
  server: { host: true, allowedHosts: ['.local'] }
  ```

## Live detection

The daemon sweeps every 2 seconds and pushes changes to the browser over
Server-Sent Events. Start a dev server and it appears within about three seconds
with no refresh, highlighted in amber for twenty seconds so you see it arrive.
Stop it and it disappears just as quickly.

Sweeps are cheap because process identity is cached per PID and never recomputed:
a steady-state sweep is one `lsof` call, around 150ms, and the daemon sits at
roughly 0.5% of one core and 17MB.

"Just started" is judged from the process's own start time, not from when the
daemon first saw it — so restarting Marina doesn't make a server that's been up
for two days light up as new.

## Postgres is optional

Postgres stores pins, nicknames, and uptime history. It is deliberately not on
the critical path: the daemon connects lazily in the background and retries with
backoff, so it starts and serves live state at login even if Postgres isn't up
yet. Pins made while it's offline are held in memory and written when it
connects. The dashboard says which state it's in rather than guessing.

The `marina` database is created on first connection. Config via `MARINA_DSN`.

## Layout

```
daemon/            Go daemon; serves the API and embeds the dashboard
  internal/scan/       lsof and ps parsing — listening sockets, cwd, uptime
  internal/identify/   socket + process → project, framework, entry script
  internal/probe/      HTTP probe: is it a web app? what's its title?
  internal/catalog/    projects on disk that could run, and the launcher
  internal/store/      Postgres with lazy connect and in-memory mirror
  internal/monitor/    the sweep loop, change detection, subscriber fan-out
    roles.go             which port is a project's front door, and which serve it
  internal/api/        JSON, SSE, favicon proxy
web/               React 19 + Vite + Tailwind 4 dashboard
menubar/           Swift menu bar agent (LSUIElement, no Dock icon)
scripts/           build, install, uninstall, dev, check
```

The daemon is a single binary with the dashboard compiled into it, so there is no
static directory to lose and nothing to serve separately.

## Development

```bash
npm run dev     # daemon on :7777 + Vite with hot reload on :5199
npm run check   # go vet, go test, tsc, both production builds
npm run build   # everything into ./dist
```

`npm run dev` needs the installed daemon stopped first:

```bash
launchctl bootout gui/$(id -u)/tech.bocchino.marina
```

## Notes on scope

- Binds to loopback only, and refuses to start on any other address. Marina
  reports on everything you're running, which is not for the network.
- Mutating endpoints reject cross-origin requests, since any page in your browser
  can reach localhost.
- Processes that speak their own wire protocol (Postgres, Redis, Mongo) are never
  probed over HTTP, to keep junk out of their logs.

## Uninstall

```bash
npm run uninstall              # removes agents, binaries, symlink
bash scripts/uninstall.sh --drop-db   # also drops the marina database
```
