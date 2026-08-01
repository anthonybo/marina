package identify

import "testing"

// TestDetectFrameworkAvoidsSubstringFalsePositives pins down the class of bug
// that naive substring matching produced: real command lines from this machine
// that a `strings.Contains` implementation misidentified.
func TestDetectFrameworkAvoidsSubstringFalsePositives(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		deps map[string]bool
		want string
	}{
		{
			// "--expose-gc" contains "expo".
			name: "expose-gc is not Expo",
			cmd:  "node --expose-gc --max-old-space-size=16384 server.js",
			want: "Node",
		},
		{
			// "bundle" contains "bun".
			name: "bundle is not Bun",
			cmd:  "node /app/scripts/bundle.js",
			want: "Node",
		},
		{
			// "observer" contains "serve".
			name: "observer is not the serve package",
			cmd:  "node /app/observer.js",
			want: "Node",
		},
		{
			// A path segment named "air" would match, a word inside one must not.
			name: "chair is not air",
			cmd:  "node /Users/me/chair/index.js",
			want: "Node",
		},
		{
			name: "vite.js resolves to Vite",
			cmd:  "node /app/node_modules/.bin/../vite/bin/vite.js",
			want: "Vite",
		},
		{
			name: "tsx preflight resolves to tsx",
			cmd:  "/usr/bin/node --require /app/node_modules/.pnpm/tsx@4.21.0/node_modules/tsx/dist/preflight.cjs src/index.ts",
			want: "tsx",
		},
		{
			name: "uvicorn resolves to FastAPI",
			cmd:  "/usr/bin/python3 -m uvicorn app.main:app --port 8000",
			want: "FastAPI",
		},
		{
			name: "django manage.py runserver",
			cmd:  "python3 manage.py runserver 0.0.0.0:8000",
			want: "Django",
		},
		{
			name: "vite with sveltekit dependency reports SvelteKit",
			cmd:  "node /app/node_modules/vite/bin/vite.js",
			deps: map[string]bool{"@sveltejs/kit": true},
			want: "SvelteKit",
		},
		{
			name: "next dev",
			cmd:  "node /app/node_modules/.bin/next dev",
			want: "Next.js",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := detectFramework(tc.cmd, tc.deps)
			if got != tc.want {
				t.Errorf("detectFramework(%q) = %q, want %q", tc.cmd, got, tc.want)
			}
		})
	}
}

// TestDetectEntry covers the signal that tells sibling workers apart.
func TestDetectEntry(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{
			name: "skips --require and --import values",
			cmd:  "/usr/bin/node --require /app/node_modules/.pnpm/tsx@4.21.0/node_modules/tsx/dist/preflight.cjs --import file:///app/node_modules/.pnpm/tsx@4.21.0/node_modules/tsx/dist/loader.mjs src/index.ts",
			want: "index.ts",
		},
		{
			name: "absolute worker path reduces to its basename",
			cmd:  "/usr/bin/node --require /app/node_modules/tsx/dist/preflight.cjs --import file:///app/loader.mjs /app/packages/backend/src/apiServer.js",
			want: "apiServer.js",
		},
		{
			name: "boolean flags do not swallow the entry",
			cmd:  "node --expose-gc --max-old-space-size=16384 server.js",
			want: "server.js",
		},
		{
			name: "dependency entry points are not user code",
			cmd:  "node /app/node_modules/.bin/../vite/bin/vite.js",
			want: "",
		},
		{
			name: "no entry at all",
			cmd:  "/usr/local/bin/redis-server *:6379",
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectEntry(tc.cmd); got != tc.want {
				t.Errorf("detectEntry() = %q, want %q", got, tc.want)
			}
		})
	}
}
