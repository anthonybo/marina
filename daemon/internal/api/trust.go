package api

import (
	"net/http"
	"os"
	"path/filepath"
)

// Trust handles the two requests a device makes before it can trust this
// dashboard: the page explaining what to do, and the CA certificate itself.
//
// # Why these must be reachable over plain HTTP
//
// The certificate Marina serves is signed by a CA that exists only on the machine
// Marina runs on. Any other device rejects it until that CA is installed — and if
// port 80 redirected everything to HTTPS, the download would be redirected to the
// very certificate the device cannot verify yet. So these two paths are the only
// exceptions to the redirect. Everything else still goes to HTTPS.
//
// # What is and is not served
//
// Only the CA's *public* certificate, and only if the installer put a copy where
// this can find it. mkcert's private key lives beside the original and is never
// read here — with it, anyone could issue a trusted certificate for any site in
// the world to whoever installed the CA. The page says as much, because a person
// clicking "install a root certificate" deserves to know what they are agreeing
// to.
type trustPaths struct{ page, cert string }

// trustRoutes are exempt from the HTTPS redirect.
var trustRoutes = trustPaths{page: "/trust", cert: "/ca.pem"}

// caPath is where the installer copies the CA's public certificate.
func caPath(stateDir string) string { return filepath.Join(stateDir, "tls", "ca.pem") }

// handleCACert serves the CA's public certificate for download.
func (s *Server) handleCACert(w http.ResponseWriter, r *http.Request) {
	body, err := os.ReadFile(caPath(s.stateDir))
	if err != nil {
		http.Error(w, "No certificate authority is installed on this machine.", http.StatusNotFound)
		return
	}
	// application/x-x509-ca-cert is what makes iOS offer to install a profile
	// rather than showing the file as text.
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="marina-local-ca.pem"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// handleTrustPage explains how to stop the warning, in plain HTML so it renders
// before anything is trusted and without the dashboard bundle.
func (s *Server) handleTrustPage(w http.ResponseWriter, r *http.Request) {
	_, err := os.Stat(caPath(s.stateDir))
	available := err == nil

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	host := r.Host
	page := `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Trust this Mac's certificate — Marina</title>
<style>
  :root { color-scheme: dark }
  body { margin:0; padding:2rem 1.25rem 4rem; background:#05161f; color:#eef7f9;
         font:16px/1.6 -apple-system, BlinkMacSystemFont, system-ui, sans-serif;
         max-width:34rem; margin-inline:auto }
  h1 { font-size:1.4rem; letter-spacing:-0.02em; margin:0 0 .25rem }
  p.sub { color:#8fb8c8; margin:0 0 2rem }
  a.dl { display:block; text-align:center; text-decoration:none; font-weight:600;
         background:#17a892; color:#04121a; padding:.85rem 1rem; border-radius:.7rem;
         margin:0 0 1.75rem }
  ol { padding-left:1.3rem } li { margin:.5rem 0 }
  code { font:13px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace;
         background:#0a2230; padding:.15rem .4rem; border-radius:.3rem }
  pre { background:#0a2230; padding:.85rem; border-radius:.55rem; overflow-x:auto }
  pre code { background:none; padding:0 }
  .warn { border:1px solid #143c4f; border-left:3px solid #ffb454;
          background:#071a24; padding:.9rem 1rem; border-radius:.5rem; margin:2rem 0 0 }
  .warn strong { color:#ffc978 }
  h2 { font-size:1rem; margin:2rem 0 .5rem; color:#adcedb }
  .none { border:1px solid #143c4f; padding:1rem; border-radius:.5rem; color:#8fb8c8 }
</style>
</head><body>
<h1>Trust this Mac's certificate</h1>
<p class="sub">So <code>` + host + `</code> loads without a warning.</p>
`
	if available {
		page += `<a class="dl" href="` + trustRoutes.cert + `">Download the certificate</a>

<h2>macOS</h2>
<ol>
  <li>Open the downloaded file — Keychain Access opens.</li>
  <li>Add it to the <strong>System</strong> keychain.</li>
  <li>Find <em>mkcert</em> in the list, open it, and under <strong>Trust</strong>
      set <em>When using this certificate</em> to <strong>Always Trust</strong>.</li>
</ol>
<p>Or, if that Mac has mkcert installed, do it in one command after copying the
file across:</p>
<pre><code>sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain marina-local-ca.pem</code></pre>

<h2>iPhone or iPad</h2>
<ol>
  <li>Tap the download — iOS saves it as a <em>profile</em>.</li>
  <li>Settings → General → VPN &amp; Device Management → install it.</li>
  <li>Then Settings → General → About → <strong>Certificate Trust Settings</strong>
      and switch it on. iOS hides this step, and the certificate does nothing
      until you do it.</li>
</ol>
`
	} else {
		page += `<p class="none">This Mac has no certificate authority to hand out, so
the dashboard is served over plain HTTP and browsers will call it
&ldquo;Not Secure&rdquo;. To change that, install mkcert on the Mac running Marina
(<code>brew install mkcert &amp;&amp; mkcert -install</code>) and run the installer
again.</p>`
	}

	page += `<div class="warn">
<p><strong>What you are agreeing to.</strong> This certificate authority lives on
one Mac. Installing it means that machine can issue a certificate your device will
trust <em>for any website</em>, not only for Marina. That is fine for a computer you
own and control, and a bad idea on a device you share or do not.</p>
<p>Only the public certificate is served here. The private key that signs with it
never leaves the Mac.</p>
</div>
</body></html>`

	_, _ = w.Write([]byte(page))
}
