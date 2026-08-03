# Deferred work

Things consciously not done, with enough context to pick up cold. Each entry says
why it was left, what is already known, and what the first move is — so none of it
has to be re-derived.

---

## A padlock on the dashboard, via a real domain

**Status:** deferred. `http://marina.local` works today and loads everywhere with
nothing installed. This is only about the grey "Not secure" chip.

**The constraint, already settled:** no public certificate authority may issue for a
`.local` name. The CA/Browser Forum's Baseline Requirements have prohibited internal
names and RFC-1918 addresses [since 1 November
2015](https://cabforum.org/working-groups/server/internal-names/), mDNS included. So
HTTPS on `marina.local` means a private CA, and a private CA means installing it on
every device before anything loads. That was tried and reverted — it looked correct
on the Mac that minted the certificate and blocked every other device with a
full-page interstitial.

**The only arrangement that gives a padlock with nothing installed on the client** is
a name a public CA will vouch for, i.e. a domain we own:

1. Pick a name on a domain we control, e.g. `marina.<domain>`.
2. Point it at the machine's LAN address. Either a public `A` record, or a local
   resolver override if the router allows one.
3. Take a certificate by **ACME DNS-01** — a DNS challenge needs no inbound
   connection, so nothing has to be exposed to the internet. In Go,
   [CertMagic](https://github.com/caddyserver/certmagic) or
   [lego](https://github.com/go-acme/lego) both do this, with per-provider DNS
   plugins for the TXT record.
4. Renewal has to be automatic, and the record has to track the LAN address when the
   DHCP lease changes — which is the same problem `marina.local` already solves, so
   the name would need updating from the same place `netinfo` reports the address.

**Test this before building it.** A public DNS record answering with a private
address is precisely what DNS-rebinding protection blocks, and it is enabled by
default on [pfSense](https://docs.netgate.com/pfsense/en/latest/services/dns/rebinding.html),
Google Home/Nest, and NextDNS among others. Where it is on, the name does not resolve
at all. One `dig` against the household resolver settles whether this route is worth
any code:

```bash
# After adding a test A record pointing at a private address:
dig +short marina.<domain> @<the router's resolver>
# An empty answer means rebinding protection stripped it — stop here.
```

**Groundwork already in place:** `--tls` serves HTTPS on 443 and redirects 80 to it,
`internal/tlscert` loads and hot-reloads a certificate pair from
`~/.local/share/marina/tls/`, and `internal/launchsock` gets the privileged port from
launchd with nothing running as root. A publicly-trusted certificate dropped into
that directory works with the code as it stands; the missing part is obtaining and
renewing it.

**Alternative without owning a domain:** [tlsmy.net](https://github.com/supersat/tlsmy.net)
and [DummyTLS](https://github.com/paullouisageneau/dummytls) encode the address into
a hostname on a shared domain and hold a real certificate for it. No install on
clients, at the cost of trusting the operator — and some publish the private key,
which removes the warning without providing privacy. Worth knowing about; not
obviously better than plain HTTP on a home network.

---

## Apps that only listen on loopback

**Status:** partly done. StormWire's frontend was fixed; the others are untouched.

An app bound to localhost cannot be opened from a phone however correct the address
is, and Vite and Astro bind localhost by default. Those show at the pier on the phone
page, untappable, with a note. Each needs two lines in its own project:

```js
server: { host: true, allowedHosts: ['.local'] }
```

`host: true` makes it listen on the network; `allowedHosts` stops Vite refusing the
Host header — raw IPv4 is always allowed but a name is not, so `.local` returns
"Blocked request" without it. A leading dot matches any host with that suffix, which
covers every Bonjour name including an alias.

Done: `iptv-epg-matcher`, `stormwire` (uncommitted in its own repo at the time of
writing). Not done: everything else with a Vite or Astro dev server.

---

## Reachability, visible at a glance on the desktop

**Status:** not started.

The phone page distinguishes reachable from loopback-only, because tapping a link
that cannot work is worse than no link. The desktop dashboard does not — the data is
there (`wildcard` on each service, already in `/api/state`) but nothing renders it, so
the only way to know is to hover the address hint in the header. A small marker per
row would make it glanceable.

---

## Port history contains ports nothing chose

**Status:** harmless, filtered on read, untidy on disk.

Ephemeral ports (≥49152) are no longer treated as a project's own port, and are
filtered out of predictions — but rows recorded before that fix are still in Postgres.
Every port on record for some projects was between 64000 and 65500, all from one-off
scripts. Purging them is a one-line delete against `sightings`; nothing depends on it.

---

## Projects with no start command Marina recognises

**Status:** open, needs a decision per project.

`client-sites` has scripts (`new`, `build`, `check`, `format`, `deploy:*`) but nothing
that looks like "start this", so it lands in the "no start command" count rather than
the boatyard. Teaching the detector needs to know which script actually starts it —
guessing would launch the wrong thing.

---

## Java and Tomcat backends

**Status:** blocked on a question.

Detection already works — anything holding a listening port is found, `java` is
recognised as a framework, and Tomcat's extra ports (8005 shutdown, 8009 AJP) cluster
behind the app rather than appearing as separate rows. Two gaps:

- `pom.xml` and `build.gradle` are not project markers, so a Maven or Gradle project
  **without** `.git` falls back to being named after its directory.
- Nothing knows how to start one, so they never appear in the boatyard.

The useful shape depends on the setup, which is the open question: **Spring Boot with
an embedded server** is straightforward (`./mvnw spring-boot:run`, `./gradlew bootRun`
— one command per project, exactly like the npm ones), whereas **a standalone Tomcat
with WARs deployed into it** has no per-project command at all; you start Tomcat, not
an app, and the best that can be done is naming it properly.

---

## Housekeeping

- **No `LICENSE`.** The repository is public with none, so default copyright applies
  and nobody may reuse it. MIT is the usual pick; it needs a deliberate choice.
- **`TestLookupOnThisMachine` logs the real hostname and LAN address** into test
  output. That is runtime data on the machine running the test, never committed, but
  it could assert without printing them.
