package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// DefaultReadOnlyDirs are the well-known roots a development toolchain must
// read and execute inside the sandbox. Missing paths are dropped silently
// (they differ per distro/platform); the spec stays honest either way.
//
// /etc is deliberately absent: the directory holds sensitive world-readable
// files (/etc/passwd, /etc/shadow, host credential material) that a dev
// toolchain never needs. ToolchainEtcFiles grants the narrow per-file set
// they actually read instead.
var DefaultReadOnlyDirs = []string{
	"/usr",
	"/bin",
	"/sbin",
	"/lib",
	"/lib64",
	"/dev",
	"/proc",
	"/sys",
	// /run is fully denied: it holds the container canonical secret
	// mounts (/run/secrets, K8s service-account tokens) and the
	// rootless docker/podman sockets under /run/user/<uid> — a socket
	// connect there is a host escape. Unix-socket connect is not
	// landlock-gated, so the directory grant is the only lever.
	// Operators needing specific runtime sockets can list them
	// explicitly via trusted_extra_roots (sub-directories stay
	// listable there).
	"/opt",
	"/nix",
	"/home/linuxbrew/.linuxbrew",
	"/Applications",
	"/Library",
	"/System",
	"/usr/local",
	"/private",
	"/Volumes",
}

// ToolchainEtcFiles are the per-file /etc rules that replace the broad
// directory grant: CA bundles and TLS trust, name resolution, the dynamic
// loader, the Debian/Ubuntu alternatives symlink farm, time zone data, and
// git's global config. Everything else under /etc — account databases,
// shadow files, ssh host keys, sudoers — stays unreadable to confined
// children. Missing paths are dropped (they differ per distro).
func ToolchainEtcFiles() []string {
	return []string{
		// TLS trust, scoped to the certificate stores and trust
		// configuration — never the enclosing /etc/ssl or /etc/pki
		// roots, which also hold private-key directories
		// (/etc/ssl/private, /etc/pki/tls/private). Recursive trust
		// grants must not include key material a service account
		// can read.
		"/etc/ssl/certs",
		"/etc/ssl/openssl.cnf",
		"/etc/pki/tls/certs",
		"/etc/pki/ca-trust",
		"/etc/ca-certificates",
		// Name resolution.
		"/etc/resolv.conf",
		"/etc/hosts",
		"/etc/nsswitch.conf",
		"/etc/host.conf",
		"/etc/gai.conf",
		"/etc/protocols",
		"/etc/services",
		// Time zone data.
		"/etc/localtime",
		"/etc/timezone",
		// Dynamic loader and Debian/Ubuntu symlink farm.
		"/etc/ld.so.cache",
		"/etc/ld.so.conf",
		"/etc/ld.so.conf.d",
		"/etc/alternatives",
		// OS identity and toolchain configuration.
		"/etc/os-release",
		"/etc/machine-id",
		"/etc/krb5.conf",
		"/etc/gitconfig",
		// GDB's system-wide initialization. GDB reads these before
		// any user init file; they are root-owned host configuration,
		// read-only here, and cannot be reached by a confined child
		// today, which is why C++ debugging failed to start.
		"/etc/gdb/gdbinit",
		"/etc/gdb/gdbinit.d",
	}
}

// userReadOnlyFiles returns single-file exceptions tools must read. git
// treats EACCES on its config files as fatal (unlike ENOENT), so these
// need file-level rules, not dir rules.
func userReadOnlyFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range []string{".gitconfig", ".gitignore_global", ".hgrc", ".npmrc"} {
		p := filepath.Join(home, f)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// DefaultReadWriteDirs are the writable locations a confined dev toolchain
// needs: the OS temp dir, and the active GOCACHE/GOMODCACHE when they live
// outside the workspace. GOMODCACHE is writable so toolchain downloads
// (GOTOOLCHAIN) don't wedge; a stricter profile can drop it later.
//
// The writable roots are directory-scoped and recursive, so the host GOPATH
// itself stays denied — that keeps 'go install' from planting binaries on
// PATH and the host checksum DB (pkg/sumdb, a sibling of pkg/mod) pristine.
// Writes Go legitimately needs there (sumdb lookups, toolchain verification)
// are served by the scratch GOPATH in [ToolchainScratchEnv].
//
// The language-runtime caches (npm, pip, cargo, maven, gradle) are NOT
// included: a cache directory is trusted input for every future build, so
// widening the writable set to it is an explicit operator decision
// (tools.bash.trust_toolchain_caches). When trusted, the narrowed cache
// roots join the writable set via ExtraTrustedRoots, keeping the kernel
// spec and the in-process pathguard on one list.
func DefaultReadWriteDirs() []string {
	var out []string
	if tmp := os.TempDir(); tmp != "" {
		out = append(out, tmp)
	}
	if cache := goCacheDir(); cache != "" {
		out = append(out, cache)
	}
	if mod := goModCacheDir(); mod != "" {
		out = append(out, mod)
	}
	return out
}

// TrustedToolchainCaches returns the toolchain cache roots the agent is
// allowed to read and write when tools.bash.trust_toolchain_caches is on:
// the narrowed language-runtime caches (LanguageCacheDirs) plus the active
// GOMODCACHE so dependency source can be inspected.
//
// The Go standard library is deliberately absent: its source lives under
// GOROOT, which is already on the kernel read-only roots, and `go doc`
// resolves stdlib and module packages through the toolchain itself — no
// pathguard widening required. Enumerating stdlib source directories
// per-language would leave the two enforcement layers unable to stay in
// lockstep, so stdlib reads route through `go doc` instead.
func TrustedToolchainCaches() []string {
	roots := LanguageCacheDirs()
	if mod := goModCacheDir(); mod != "" {
		roots = append(roots, mod)
	}
	return roots
}

// LanguageCacheDirs returns the cache roots for the common language
// toolchains, honoring each tool's canonical override variable
// (npm_config_cache, PIP_CACHE_DIR, CARGO_HOME, MAVEN_HOME,
// GRADLE_USER_HOME, UV_CACHE_DIR, BUN_INSTALL, XDG_DATA_HOME,
// NUGET_PACKAGES, CCACHE_DIR) the same way the tools themselves do.
// Home-relative fallbacks follow each tool's default.
//
// The roots are deliberately scoped to cache content only: cargo gets
// registry/ and git/ but not ~/.cargo itself (excluding the bin/ dir that
// sits on PATH, the credentials file, and config.toml, which can redirect
// the registry), maven gets repository/ but not settings.xml, and gradle
// gets caches/, wrapper/, daemon/, and native/ but not gradle.properties
// or init.d/ (any file dropped in init.d/ executes on every future host
// Gradle build). Credential files under the excluded parents stay neither
// writable nor readable in the sandbox, and the same list feeds the
// in-process pathguard so both layers agree on what is trusted.
func LanguageCacheDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	cargoHome := firstNonEmpty(os.Getenv("CARGO_HOME"), filepath.Join(home, ".cargo"))
	mavenHome := firstNonEmpty(os.Getenv("MAVEN_HOME"), filepath.Join(home, ".m2"))
	gradleHome := firstNonEmpty(os.Getenv("GRADLE_USER_HOME"), filepath.Join(home, ".gradle"))
	xdgData := firstNonEmpty(os.Getenv("XDG_DATA_HOME"), filepath.Join(home, ".local", "share"))
	bunHome := firstNonEmpty(os.Getenv("BUN_INSTALL"), filepath.Join(home, ".bun"))
	return []string{
		firstNonEmpty(os.Getenv("npm_config_cache"), filepath.Join(home, ".npm")),
		firstNonEmpty(os.Getenv("PIP_CACHE_DIR"), filepath.Join(home, ".cache", "pip")),
		firstNonEmpty(os.Getenv("UV_CACHE_DIR"), filepath.Join(home, ".cache", "uv")),
		// ccache.conf lives inside the granted dir and supports
		// prefix_command/prefix_command_cpp (arbitrary program
		// execution on every cached compile) — another reason the
		// whole list stays gated behind trust_toolchain_caches.
		firstNonEmpty(os.Getenv("CCACHE_DIR"), filepath.Join(home, ".cache", "ccache")),
		filepath.Join(cargoHome, "registry"),
		filepath.Join(cargoHome, "git"),
		filepath.Join(mavenHome, "repository"),
		filepath.Join(gradleHome, "caches"),
		filepath.Join(gradleHome, "wrapper"),
		// Gradle daemon logs and extracted native libraries live in
		// dedicated subdirs; neither carries executable build logic, so
		// granting them does not reopen the init.d/ or gradle.properties
		// vectors.
		filepath.Join(gradleHome, "daemon"),
		filepath.Join(gradleHome, "native"),
		filepath.Join(bunHome, "install", "cache"),
		filepath.Join(xdgData, "pnpm", "store"),
		firstNonEmpty(os.Getenv("NUGET_PACKAGES"), filepath.Join(home, ".nuget", "packages")),
	}
}

// TrustedCacheFiles returns single-file writable rules the toolchain caches
// need on top of [LanguageCacheDirs]'s directory roots: Cargo's cache locks
// (.package-cache for downloads/resolution, .package-cache-mutate for
// builds and cache mutation). Both live at the CARGO_HOME root, which stays
// denied — only registry/ and git/ are granted — so cargo takes its Shared
// lock file on every plain build and would wedge on the deny without them.
//
// Kernel file rules (Landlock) need an O_PATH fd, so the files must exist
// before the spec is built: this function creates them, but only when
// CARGO_HOME already exists, keeping a fresh cargo-less host untouched.
// Cargo recreates the locks itself after they exist once.
func TrustedCacheFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	cargoHome := firstNonEmpty(os.Getenv("CARGO_HOME"), filepath.Join(home, ".cargo"))
	if _, err := os.Stat(cargoHome); err != nil {
		return nil
	}
	locks := []string{
		filepath.Join(cargoHome, ".package-cache"),
		filepath.Join(cargoHome, ".package-cache-mutate"),
	}
	// Only existing files go into the spec: Landlock needs an O_PATH fd per
	// rule, so a missing path would fail confinement instead of degrading.
	var existingLocks []string
	for _, lock := range locks {
		if _, err := os.Stat(lock); err != nil {
			f, err := os.OpenFile(lock, os.O_CREATE|os.O_APPEND, 0o600)
			if err != nil {
				continue
			}
			_ = f.Close()
		}
		existingLocks = append(existingLocks, lock)
	}
	return existingLocks
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// CredentialDenyPaths returns the built-in deny-read set: well-known
// credential locations that a confined child must never read. Missing
// paths are kept (unlike the read-only roots) so a credential file
// created mid-session stays protected. The list is home-relative and
// covers the POSIX and Windows layouts; the in-process pathguard reads
// the same Spec so both layers agree on what is secret.
//
// ~/.npmrc is deliberately absent: userReadOnlyFiles grants it read so
// git/npm registry auth keeps working, and the grant is intentional.
func CredentialDenyPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	paths := []string{
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".aws"),
		filepath.Join(home, ".azure"),
		filepath.Join(home, ".gnupg"),
		filepath.Join(home, ".git-credentials"),
		filepath.Join(home, ".netrc"),
		filepath.Join(home, ".kube"),
		filepath.Join(home, ".config", "gcloud"),
		filepath.Join(home, ".config", "gh"),
		// Podman's registry credentials (auth.json) live under
		// ~/.config/containers, the podman analogue of the denied
		// ~/.docker/config.json.
		filepath.Join(home, ".config", "containers"),
		filepath.Join(home, ".docker", "config.json"),
		// Maven and Gradle credential files: LanguageCacheDirs scopes
		// its grants to cache content and documents these files as the
		// excluded credential/config layer; the deny entry carries that
		// exclusion to the macOS global-read layer and the pathguard.
		filepath.Join(home, ".m2", "settings.xml"),
		filepath.Join(home, ".gradle", "gradle.properties"),
		// Token-bearing config dirs for IaC and cloud-native CLIs;
		// same posture as the registry auth files above.
		filepath.Join(home, ".terraform.d"),
		filepath.Join(home, ".oci"),
		filepath.Join(home, ".fly"),
		filepath.Join(home, ".config", "supabase"),
	}
	// Cargo credentials live directly in CARGO_HOME, the parent of the
	// registry/ and git/ cache roots LanguageCacheDirs grants. The scoped
	// cache roots keep them untrusted, but a deny entry makes the intent
	// explicit and blocks any future widening (trusted_extra_roots or a
	// mis-set CARGO_HOME) from silently exposing them.
	cargoHome := firstNonEmpty(os.Getenv("CARGO_HOME"), filepath.Join(home, ".cargo"))
	xdgData := firstNonEmpty(os.Getenv("XDG_DATA_HOME"), filepath.Join(home, ".local", "share"))
	paths = append(paths,
		filepath.Join(cargoHome, "credentials.toml"),
		filepath.Join(cargoHome, "credentials"),
		// uv's plaintext credential store (uv >= 0.8.15) lives under
		// XDG_DATA_HOME/uv/credentials. The cache grant
		// (~/.cache/uv) never covers it, but the deny entry keeps it
		// unreadable under the macOS global-read layer and blocks any
		// future widening from exposing it. uv itself reads ~/.netrc
		// always, and .netrc is already denied above.
		filepath.Join(xdgData, "uv", "credentials"),
		// Python (PyPI) legacy credential file; uv and pip read it
		// alongside .netrc, which is denied above.
		filepath.Join(home, ".pypirc"),
		// Ruby/RubyGems API keys and Bundler's config, which routinely
		// holds private gem-server tokens.
		filepath.Join(home, ".gem", "credentials"),
		filepath.Join(home, ".bundle", "config"),
		// GDB's user init file executes code on startup. Writability
		// is already impossible (home is never writable to confined
		// children); the deny closes the macOS global-read layer too,
		// so a confined child can neither plant nor stage content in
		// future host gdb sessions.
		filepath.Join(home, ".gdbinit"),
	)
	// Composer resolves its home dir (auth.json lives there) by
	// COMPOSER_HOME, then the first existing XDG config dir, then
	// ~/.composer; the platform default is checked last so a
	// non-XDG-layout host (macOS, bare installs) is covered too.
	composerHome := os.Getenv("COMPOSER_HOME")
	if composerHome == "" {
		xdgConfig := firstNonEmpty(os.Getenv("XDG_CONFIG_HOME"), filepath.Join(home, ".config"))
		for _, candidate := range []string{
			filepath.Join(xdgConfig, "composer"),
			filepath.Join(home, ".composer"),
		} {
			if _, err := os.Stat(candidate); err == nil {
				composerHome = candidate
				break
			}
		}
		if composerHome == "" {
			switch runtime.GOOS {
			case "windows":
				composerHome = filepath.Join(home, "AppData", "Roaming", "Composer")
			case "darwin":
				composerHome = filepath.Join(home, ".composer")
			default:
				composerHome = filepath.Join(xdgConfig, "composer")
			}
		}
	}
	paths = append(paths, filepath.Join(composerHome, "auth.json"))
	// Dart/Flutter's pub credential store; macOS keeps it under
	// Application Support, which the global-read layer would otherwise
	// expose.
	paths = append(paths, filepath.Join(home, ".config", "dart", "pub-credentials.json"))
	// Conan v2 resolves its home via CONAN_HOME (default ~/.conan2);
	// credentials.json, source_credentials.json, and the plaintext
	// login-token database all live at that root. CONAN_USER_HOME is the
	// v1-era spelling of the same override.
	conanHome := firstNonEmpty(os.Getenv("CONAN_HOME"), os.Getenv("CONAN_USER_HOME"))
	if conanHome == "" {
		conanHome = filepath.Join(home, ".conan2")
	}
	paths = append(paths,
		filepath.Join(conanHome, "credentials.json"),
		filepath.Join(conanHome, "source_credentials.json"),
		filepath.Join(conanHome, ".conan.db"),
	)
	// Conan v1 home (~/.conan, CONAN_USER_HOME override): the v1
	// credential localdb is .conan.db, and conan.conf/registry.json carry
	// remote endpoints and proxy configuration.
	conanV1Home := firstNonEmpty(os.Getenv("CONAN_USER_HOME"), filepath.Join(home, ".conan"))
	paths = append(paths,
		filepath.Join(conanV1Home, ".conan.db"),
		filepath.Join(conanV1Home, "conan.conf"),
		filepath.Join(conanV1Home, "registry.json"),
	)
	// sccache's config (SCCACHE_CONF override) carries S3/GCS/Azure
	// bucket credentials for the remote cache.
	sccacheConf := os.Getenv("SCCACHE_CONF")
	if sccacheConf == "" {
		switch runtime.GOOS {
		case "darwin":
			sccacheConf = filepath.Join(home, "Library", "Application Support", "Mozilla.sccache", "config")
		case "windows":
			sccacheConf = filepath.Join(home, "AppData", "Roaming", "Mozilla", "sccache", "config", "config")
		default:
			sccacheConf = filepath.Join(firstNonEmpty(os.Getenv("XDG_CONFIG_HOME"), filepath.Join(home, ".config")), "sccache", "config")
		}
	}
	paths = append(paths, sccacheConf)
	// NuGet user-level configs hold feed credentials; the dotnet CLI and
	// NuGet.exe/Mono resolve the user-level file to different paths, so
	// both are denied.
	paths = append(paths,
		filepath.Join(home, ".nuget", "NuGet", "NuGet.Config"),
		filepath.Join(home, ".config", "NuGet", "NuGet.Config"),
	)
	switch runtime.GOOS {
	case "darwin":
		paths = append(paths,
			filepath.Join(home, "Library", "Keychains"),
			filepath.Join(home, ".config", "huggingface"),
			// Dart/Flutter's pub credential store on macOS lives under
			// Application Support; the global-read layer makes the
			// deny entry the entire defense.
			filepath.Join(home, "Library", "Application Support", "dart", "pub-credentials.json"),
			// macOS app-data stores. Seatbelt allows global reads by
			// design, so the deny list is the entire defense for
			// browser sessions, chat apps, and the TCC database.
			// Curated per-app rather than a blanket Application
			// Support deny: dev tooling legitimately stores config
			// there (and the workspace's own tooling must keep
			// working); the listed apps hold session tokens and
			// personal data a dev-tool child has no business
			// reading.
			filepath.Join(home, "Library", "Application Support", "Google", "Chrome"),
			filepath.Join(home, "Library", "Application Support", "Microsoft Edge"),
			filepath.Join(home, "Library", "Application Support", "Firefox"),
			filepath.Join(home, "Library", "Safari"),
			filepath.Join(home, "Library", "Cookies"),
			filepath.Join(home, "Library", "Messages"),
			filepath.Join(home, "Library", "Application Support", "Slack"),
			filepath.Join(home, "Library", "Application Support", "Signal"),
			filepath.Join(home, "Library", "Application Support", "WhatsApp"),
			// VS Code session storage holds OAuth session tokens for
			// GitHub/Microsoft accounts.
			filepath.Join(home, "Library", "Application Support", "Code"),
			// TCC (privacy/permissions database).
			filepath.Join(home, "Library", "Application Support", "com.apple.TCC"),
		)
	case "windows":
		appdata := filepath.Join(home, "AppData", "Roaming")
		local := filepath.Join(home, "AppData", "Local")
		paths = append(paths,
			filepath.Join(appdata, "gcloud"),
			filepath.Join(appdata, "docker", "config.json"),
			filepath.Join(home, ".docker", "config.json"),
			// Dart/Flutter's pub credential store follows the same
			// XDG-style layout on Windows; the deny keeps intent
			// explicit for the pathguard.
			filepath.Join(home, ".config", "dart", "pub-credentials.json"),
			// NuGet's user-level config on Windows lives under
			// %APPDATA%\NuGet (the Linux/macOS user-level paths are
			// denied unconditionally above).
			filepath.Join(appdata, "NuGet", "NuGet.Config"),
			// Windows Credential Manager DPAPI blobs. AppContainer
			// default-denies them (ungranted paths are unreadable),
			// so this is pathguard-level defense-in-depth plus
			// documented intent. Registry credential access is
			// registry-ACL-governed and not expressible with path
			// rules; see [SpawnConfined] on what the kernel boundary
			// does and does not cover.
			filepath.Join(local, "Microsoft", "Credentials"),
			filepath.Join(appdata, "Microsoft", "Credentials"),
		)
	}
	// System-wide credential and identity paths. On Linux the Landlock
	// default-deny already covers these; the entries carry the denial to
	// the macOS seatbelt layer (which allows global reads by design) and
	// the in-process pathguard, and document intent. The Kubernetes
	// service-account token and the /run/secrets mount point are here
	// because a confined child must never hold cluster credentials even
	// if future policy widens /var or /run access.
	paths = append(paths,
		"/etc/passwd",
		"/etc/shadow",
		"/etc/group",
		"/etc/sudoers",
		"/etc/sudoers.d",
		"/etc/ssh",
		"/run/secrets",
		"/var/run/secrets/kubernetes.io/serviceaccount",
	)
	return paths
}

// confinedAgentSocketVars are environment variables that hand a confined
// child a live path to the user's ssh-agent. The socket itself usually
// lives in /tmp (a writable root) or /run/user/<uid>, so the kernel
// boundary alone cannot stop a confined child from asking the agent to
// sign with keys the sandbox can't read. Stripping the variables removes
// the pointer; ssh falls back to the on-disk identity files, which
// CredentialDenyPaths already denies, so this is strictly no weaker.
var confinedAgentSocketVars = map[string]struct{}{
	"SSH_AUTH_SOCK": {},
	"SSH_AGENT_PID": {},
}

// ConfinedEnv returns env with the ssh-agent socket variables removed.
// The result is a fresh slice; env is never modified. Windows environment
// names are case-insensitive, so a `Ssh_Auth_Sock` variant is the same
// variable there and is stripped case-insensitively; POSIX envs are
// case-sensitive and match exactly.
func ConfinedEnv(env []string) []string {
	fold := runtime.GOOS == "windows"
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		_, deny := confinedAgentSocketVars[key]
		if !deny && fold {
			_, deny = confinedAgentSocketVars[strings.ToUpper(key)]
		}
		if deny {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// SpecFor builds the workspace-mode sandbox policy. The workspace root is
// always writable; ExtraTrustedRoots (pathguard's trusted additions) join
// the writable set; well-known read-only roots are filtered to paths that
// actually exist on this host.
func SpecFor(workspace string, extraTrustedRoots []string) Spec {
	return SpecForLimits(workspace, extraTrustedRoots, ResourceLimits{})
}

// SpecForLimits is SpecFor with explicit resource caps applied to the
// confined child.
func SpecForLimits(workspace string, extraTrustedRoots []string, limits ResourceLimits) Spec {
	return Spec{
		WorkspaceRoot:  workspace,
		ReadOnlyRoots:  existing(append(append([]string{}, DefaultReadOnlyDirs...), ToolchainEtcFiles()...)),
		ReadWriteRoots: append(append([]string{}, DefaultReadWriteDirs()...), extraTrustedRoots...),
		ReadOnlyFiles:  userReadOnlyFiles(),
		ReadWriteFiles: append([]string{"/dev/null"}, TrustedCacheFiles()...),
		DenyRead:       CredentialDenyPaths(),
		Limits:         limits,
	}
}

// ForBashMode builds the sandbox policy for a managed child (bash tool,
// MCP stdio server) when kernel sandboxing is requested. Returns nil when
// sandboxing is off, invalid, or no workspace is configured.
func ForBashMode(mode, workspace string) *Spec {
	if !ModeEnabled(mode) || workspace == "" {
		return nil
	}
	spec := SpecFor(workspace, nil)
	return &spec
}

// ForBashModeLimits is ForBashMode with explicit resource caps.
func ForBashModeLimits(mode, workspace string, limits ResourceLimits) *Spec {
	if !ModeEnabled(mode) || workspace == "" {
		return nil
	}
	spec := SpecForLimits(workspace, nil, limits)
	return &spec
}

// ValidateDenyRead reports deny-read paths the Landlock backend cannot
// enforce: deny rules live outside the allow-list, so any overlap
// between a deny path and an allowed root is unenforceable and must
// fail loudly rather than silently weaken the policy (Codex
// config-validation style). Returns nil when every deny path is
// enforceable.
func (s Spec) ValidateDenyRead() error {
	roots := append([]string{s.WorkspaceRoot}, s.ReadOnlyRoots...)
	roots = append(roots, s.ReadWriteRoots...)
	roots = append(roots, s.ReadOnlyFiles...)
	roots = append(roots, s.ReadWriteFiles...)
	sep := string(filepath.Separator)
	for _, deny := range s.DenyRead {
		for _, root := range roots {
			if root == "" {
				continue
			}
			if deny == root || strings.HasPrefix(deny, root+sep) {
				return fmt.Errorf(
					"deny-read path %q is inside allowed root %q; remove one of them",
					deny, root)
			}
			if strings.HasPrefix(root, deny+sep) {
				return fmt.Errorf(
					"allowed root %q is inside deny-read path %q; remove one of them",
					root, deny)
			}
		}
	}
	return nil
}

func existing(paths []string) []string {
	var out []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func goCacheDir() string {
	if v := os.Getenv("GOCACHE"); v != "" {
		return v
	}
	if cache := cacheDir(); cache != "" {
		return filepath.Join(cache, "go-build")
	}
	return ""
}

func goModCacheDir() string {
	if v := os.Getenv("GOMODCACHE"); v != "" {
		return v
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		return filepath.Join(filepath.SplitList(gopath)[0], "pkg", "mod")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "go", "pkg", "mod")
	}
	return ""
}

func cacheDir() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cache")
	}
	return ""
}

// ToolchainScratchEnv returns the environment entries that isolate the Go
// toolchain's writable scratch state for confined children: a session-
// lifetime scratch GOPATH under the OS temp dir plus the host GOCACHE and
// GOMODCACHE pinned explicitly.
//
// The scratch GOPATH is what unblocks module and toolchain downloads that
// need a checksum-DB lookup ($GOPATH/pkg/sumdb) or a verified GOTOOLCHAIN
// download (toolchain downloads cannot run with GOSUMDB=off): the host
// GOPATH itself stays denied so `go install` cannot plant binaries on PATH
// and the host checksum DB stays pristine. The scratch dir is created under
// os.TempDir(), which [DefaultReadWriteDirs] already makes writable, so no
// spec widening is required.
//
// GOMODCACHE and GOCACHE are pinned from the same host values
// [DefaultReadWriteDirs] makes writable; without them Go would re-derive
// both from the scratch GOPATH and re-download gigabytes of modules and
// toolchains. Values that resolve empty are omitted so Go falls back to its
// own defaults rather than to an invalid pinned path.
//
// The scratch dir is created lazily on first use and is meant to live for
// the process lifetime; [CleanupToolchainScratch] removes it at shutdown
// without triggering lazy creation on runs that never spawned a shell.
func ToolchainScratchEnv() []string {
	scratch := toolchainScratchGopath()
	if scratch == "" {
		return nil
	}
	// Without a resolvable host module cache, pinning GOPATH to the scratch
	// dir would let Go create <scratch>/pkg/mod and duplicate the host
	// module cache into /tmp — skip the injection entirely instead.
	if goModCacheDir() == "" {
		return nil
	}
	env := []string{"GOPATH=" + scratch}
	if mod := goModCacheDir(); mod != "" {
		env = append(env, "GOMODCACHE="+mod)
	}
	if cache := goCacheDir(); cache != "" {
		env = append(env, "GOCACHE="+cache)
	}
	return env
}

// CleanupToolchainScratch removes the scratch GOPATH created by
// [ToolchainScratchEnv]. It is a no-op when the scratch dir was never
// created, so deferring it at shutdown cannot itself trigger lazy creation.
func CleanupToolchainScratch() {
	// The empty Do waits for any in-flight lazy creation to settle without
	// creating the dir itself, so cleanup never triggers lazy creation and
	// never races the once-value read.
	toolchainScratchOnce.Do(func() {})
	if toolchainScratchDir != "" {
		_ = os.RemoveAll(toolchainScratchDir)
		toolchainScratchOnce = sync.Once{}
		toolchainScratchDir = ""
		toolchainScratchErr = nil
	}
}

// toolchainScratchGopath lazily creates the scratch GOPATH. Creation
// happens in the Phosphor process (outside the sandbox), and os.MkdirTemp
// succeeds even when the host GOPATH is unwritable, so there is nothing to
// preflight. The unpredictable MkdirTemp name keeps concurrent Phosphor
// sessions from sharing (and one session's shutdown from deleting) another
// session's scratch state.
func toolchainScratchGopath() string {
	toolchainScratchOnce.Do(func() {
		toolchainScratchDir, toolchainScratchErr = os.MkdirTemp("", "phosphor-gopath-*")
	})
	if toolchainScratchErr != nil {
		return ""
	}
	return toolchainScratchDir
}

// toolchainScratchOnce makes lazy creation race-free: concurrent confined
// shells see either the created dir or the creation error, never a half-
// applied state. toolchainScratchDir is "" until (and unless) creation
// succeeds, which is what makes CleanupToolchainScratch a no-op before any
// confined shell ran.
var (
	toolchainScratchOnce sync.Once
	toolchainScratchDir  string
	toolchainScratchErr  error
)

// Confine wraps cmd's argv through the platform isolator if the sandbox is
// active for this spec. mode=off leaves cmd untouched; workspace degrades
// to no-sandbox with degraded=true (caller warns); strict returns the
// error.
func Confine(cmd *exec.Cmd, mode string, spec Spec) (degraded bool, err error) {
	switch strings.ToLower(mode) {
	case "", "off":
		return false, nil
	case "workspace", "strict":
		if err := spec.Limits.Validate(); err != nil {
			return false, err
		}
		iso := Current()
		if ok, _ := iso.Available(); !ok {
			if mode == "strict" {
				return false, &SandboxUnavailableError{Reason: "platform sandbox unavailable"}
			}
			// Degraded workspace mode still gets whatever partial
			// kernel containment the platform can apply (Windows
			// restricted token); the caller surfaces the warning.
			_ = iso.Confine(cmd, spec)
			return true, nil
		}
		if err := iso.Confine(cmd, spec); err != nil {
			if mode == "strict" {
				return false, err
			}
			// Degrade per-tier: drop the network posture the kernel
			// cannot enforce and retry with the FS boundary only, so
			// workspace mode keeps the strongest enforcement the host
			// supports instead of dropping to in-process guards.
			relaxed := spec
			relaxed.Network = NetworkAllow
			relaxed.ProxyPort = 0
			if err2 := iso.Confine(cmd, relaxed); err2 != nil {
				return true, nil
			}
			return true, nil
		}
		return false, nil
	default:
		return false, nil
	}
}

// ManagedConfine confines a long-lived managed child (MCP stdio server)
// with a policy built once at startup. strict mode returns the error so
// the caller refuses the connection; workspace mode confines with the
// strongest posture the host supports and reports degraded=true when the
// policy could not attach fully. A nil spec leaves the command untouched.
func ManagedConfine(cmd *exec.Cmd, mode string, spec *Spec) (degraded bool, err error) {
	if spec == nil {
		return false, nil
	}
	return Confine(cmd, mode, *spec)
}

// SandboxUnavailableError is returned in strict mode when enforcement
// cannot attach. Callers must treat it as a fatal exec refusal.
type SandboxUnavailableError struct{ Reason string }

func (e *SandboxUnavailableError) Error() string {
	return "sandbox enforcement unavailable: " + e.Reason
}

// Available reports platform sandbox availability for phosphor_info.
func Available() (ok bool, detail string) {
	return Current().Available()
}

// ModeEnabled reports whether sandboxing is requested for this mode string.
func ModeEnabled(mode string) bool {
	return strings.EqualFold(mode, "workspace") || strings.EqualFold(mode, "strict")
}
