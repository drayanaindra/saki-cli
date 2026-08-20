// Command server is the Go orchestrator backend (E14 · P1 MVP). It serves the Go-owned run
// vertical on :8788, coexisting with the TS apps/server on :8787 (which keeps build runs + the
// auto-resume engine + every un-ported route). Slice 1: GET /api/branch. Slice 2: POST /api/run
// (non-build spawn; build forwarded), GET /api/runs (union), POST /api/runs (forward).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/drayanaindra/saki-cli/backend/adapter"
	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/infra"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// realClock is the production Clock (millis since epoch, matching apps/server startedAt).
type realClock struct{}

func (realClock) Now() int64 { return time.Now().UnixMilli() }

// defaultSleeper schedules an auto-resume successor after a backoff delay. time.AfterFunc runs the
// callback on its own goroutine and never blocks the process from exiting (parity with the TS unref'd
// setTimeout — the durable pendingResume + the Sweep are the restart-safe backstop, so a dropped timer
// is recovered, not lost).
func defaultSleeper(delayMs int64, fire func()) {
	time.AfterFunc(time.Duration(delayMs)*time.Millisecond, fire)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8788"
	}
	// I8: UNSET now means STANDALONE (no apps/server upstream), not "default to :8787". The dev studio
	// pins it explicitly in package.json's dev:backend script, and production pins it in
	// deploy/systemd/saki-studio-backend.service — so flipping the default only affects a bare launch,
	// which is exactly the case the extracted saki-cli ships.
	// Trim HERE, once, so every later read of `upstream` — the proxy choice AND the startup log —
	// agrees on what "unset" means. They diverged before: selectProxy trimmed, the log did not, so
	// SAKI_UPSTREAM="   " ran standalone while logging `upstream    `. The log is the dev-studio
	// regression check (the e2e suite cannot detect the difference), so a log that disagrees with the
	// wiring is the one failure this line must never produce.
	upstream := strings.TrimSpace(os.Getenv("SAKI_UPSTREAM"))

	// Go journals live in their OWN subdir so apps/server's flat readdir never adopts them (Inv-1).
	journal := infra.NewFileJournal(infra.GoRunsDir())
	store := infra.NewMemStore()
	spawner := infra.NewShSpawner(journal)
	proxy := selectProxy(upstream)

	journalReader := infra.NewFileJournalReader(journal)
	exitWatcher := infra.NewFileExitWatcher(journal)
	// Restart-rehydrate: rebuild in-flight runs from the journals so a restart never loses one (Inv-2).
	if n, err := usecase.Rehydrate(store, journalReader); err != nil {
		log.Printf("rehydrate: %v", err)
	} else if n > 0 {
		log.Printf("rehydrated %d run(s) from %s", n, infra.GoRunsDir())
	}

	fileOutput := infra.NewFileOutput(journal)
	branchSvc := usecase.NewBranchService(infra.GitCLI{})
	runSvc := usecase.NewRunService(spawner, journal, store, realClock{}, infra.UUIDGen{})
	// F4 content service is also the source of the build engine's progress fingerprint (it owns the
	// .build-progress.md + state.json reads); construct it before the engine so it can be injected.
	contentFS := infra.ContentFS{}
	workitemsSvc := usecase.NewWorkItemsService(contentFS, infra.GitCommitVerifier{})
	// F6 · P5 slice 2: the progress-tied circuit-breaker fingerprint. The lane key is the build's PRD
	// path; a nil return (absent/unreadable scratchpad) is neutral → the engine falls back to the caps.
	buildFingerprint := func(run domain.Run) *string {
		if run.Cwd == nil || run.Meta == nil {
			return nil
		}
		return workitemsSvc.ProgressFingerprint(*run.Cwd, run.Meta.LaneKey)
	}
	// F6 · P5 slice 1-5: build spawn + the auto-resume/retry engine (Go-owned builds), with the slice-2
	// progress fingerprint and the slice-5 auto-push (GitCLI, never --force) injected.
	engineSvc := usecase.NewBuildEngineService(spawner, journal, store, realClock{}, infra.UUIDGen{}, infra.OSKiller{}, fileOutput, buildFingerprint, defaultSleeper, infra.GitCLI{}, usecase.DefaultBuildConfig())
	// F6 · P5 slice 6: build restart survival. Re-attach a survived build (exactly-one-live), resume a
	// dead-without-exit one, and rebuild the idempotency map from the journals — must run after the engine
	// + generic Rehydrate and BEFORE serving, so it claims only survived builds, not new local spawns.
	engineSvc.RehydrateBuilds(context.Background(), journalReader, exitWatcher)
	// Reap restart-SURVIVED NON-build runs (builds are reaped by the engine above; the reaper skips them).
	usecase.NewReaperService(store, journal, exitWatcher, 0).ReapExisting(context.Background())
	listSvc := usecase.NewListService(store, proxy, fileOutput)
	streamSvc := usecase.NewStreamService(store, fileOutput, realClock{}, 0)
	stopSvc := usecase.NewStopService(store, infra.OSKiller{})
	gitWriteSvc := usecase.NewGitWriteService(infra.GitCLI{}) // F3: git-write bucket (branch list + switch)

	// F4: content bucket board reads (roadmap + workitems). contentFS + workitemsSvc are declared above
	// (the build engine's fingerprint reuses workitemsSvc), served through read-only fs + git ports.
	roadmapSvc := usecase.NewRoadmapService(contentFS)
	prdSvc := usecase.NewPrdService(contentFS)                              // F4 slice 2: PRD + review reads
	lockSvc := usecase.NewLockService(contentFS, contentFS, infra.GitCLI{}) // F4 slice 3: PRD lock write
	blockersSvc := usecase.NewBlockersService(contentFS)                    // F4 slice 4: blockers read
	sliceMetaSvc := usecase.NewSliceMetaService(infra.GitCLI{})             // F4 slice 4: slice-meta read
	resolveSvc := usecase.NewResolveBlockerService(contentFS, contentFS)    // F4 slice 5: resolve-blocker write
	planTrackSvc := usecase.NewPlanTrackService(contentFS, contentFS)       // F4 slice 6: plan-track roadmap writes
	doctorSvc := usecase.NewDoctorService(infra.EngineProofChecker{})       // F2 slice 1: engine provisioning verdict
	initEnvSvc := usecase.NewInitEnvService(infra.EngineProvisioner{}, infra.EngineProofChecker{})

	// F6 slice 1: the NEW periodic sweep — fires due auto-resumes (the restart-safe backstop for the
	// in-memory timers). Go has no recurring all-runs poll otherwise; slice 3 adds the stall watchdog here.
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			engineSvc.Sweep()
		}
	}()

	h := adapter.NewHandler(branchSvc, runSvc, engineSvc, listSvc, streamSvc, stopSvc, proxy, gitWriteSvc, roadmapSvc, workitemsSvc, prdSvc, lockSvc, blockersSvc, sliceMetaSvc, resolveSvc, planTrackSvc, doctorSvc, initEnvSvc)
	mux := h.Routes()
	// F5 · P4 slice 1: proto asset serve (GET /api/proto/{dir}/{rest...}) — mounted as a sub-handler onto
	// the same mux (avoids a 16th NewHandler arg), OriginGuard-wrapped, reusing the read-only content FS.
	adapter.NewProtoHandler(usecase.NewProtoAssetService(contentFS)).Register(mux)
	// F5 · P4 slice 2: screenshot list + single-screenshot serve (GET /api/screenshots, /api/screenshot) —
	// same sub-handler pattern, OriginGuard-wrapped, reusing the content FS byte-serve path + NEW depth-4 walk.
	adapter.NewScreenshotHandler(usecase.NewScreenshotService(contentFS)).Register(mux)
	// F7 · P6 slice 2: the residual read LOCAL routes ported from apps/server (GET /api/profiles,
	// /api/env-state, /api/pick-folder) — same sub-handler pattern, OriginGuard-wrapped (parity with the
	// TS global originGuard over local routes). pick-folder is gated to darwin by runtime.GOOS.
	// F7 · P6 slice 3 adds POST /api/new-project (scaffold + git init) to the same sub-handler — its home
	// dir is resolved once here for ~ expansion (a resolve error yields "" → base validation 400s).
	projectHome, _ := os.UserHomeDir()
	adapter.NewResidualHandler(
		usecase.NewProfilesService(infra.OSProfileScanner{}),
		usecase.NewEnvStateService(infra.OSEnvStateFS{}),
		usecase.NewPickFolderService(infra.OSAScript{}, runtime.GOOS),
		usecase.NewNewProjectService(infra.OSProjectFS{}, projectHome),
	).Register(mux)
	// F7 · P6 slice 4: bind the LOOPBACK interface only (was all-interfaces ":port"). The privileged
	// kind:init spawn (--dangerously-skip-permissions) is now Go-owned, so the elevated flag must be
	// unreachable off-host — a 127.0.0.1 bind refuses any non-loopback connection at the network layer
	// (§10 rule 5 / AC 4.2). The Vite proxy targets http://127.0.0.1:8788 to match this IPv4 bind.
	addr := loopbackAddr(port)
	tcp, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		if err := http.Serve(tcp, mux); err != nil && err != http.ErrServerClosed {
			log.Printf("tcp serve: %v", err)
		}
	}()

	unix, socketPath, err := listenUnix(daemonSocketPath())
	if err != nil {
		// A socket the CLI cannot get is a DEGRADATION, not a startup failure: the TCP listener above
		// is already serving and the CLI falls back to it whenever socketPath is absent from the state.
		log.Printf("unix socket disabled: %v", err)
	}
	if unix != nil {
		go func() {
			if err := http.Serve(unix, mux); err != nil && err != http.ErrServerClosed {
				log.Printf("unix serve: %v", err)
			}
		}()
	}
	writeDaemonState(socketPath, addr)
	log.Printf("saki go backend listening on http://%s (%s), socket=%s", addr, proxyMode(upstream), socketPath)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	if unix != nil {
		_ = unix.Close()
		_ = os.Remove(socketPath)
	}
	_ = tcp.Close()
	// §10 rule 6: this cleanup runs in the handler BODY, never via defer — defer does not execute
	// through os.Exit.
	removeOwnDaemonState()
	os.Exit(0)
}

func daemonStatePath() string {
	if path := strings.TrimSpace(os.Getenv("SAKI_DAEMON_STATE_PATH")); path != "" {
		return path
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("saki-%d", os.Getuid()), "backend.state.json")
}

func daemonSocketPath() string { return filepath.Join(filepath.Dir(daemonStatePath()), "backend.sock") }

// listenUnix binds the owner-local unix socket that sits alongside the loopback TCP listener
// (🔒 INVARIANT 1 — the socket is owner-only, never off-host).
//
// Returns the listener and the path actually bound; an empty path means "no socket, use TCP" and is
// the CLI's signal to fall back. Windows is a declared Non-Goal, so it takes that path deliberately.
//
// Ordering is load-bearing:
//   - the length guard runs BEFORE Listen, so an over-long path is a descriptive error rather than a
//     bare EINVAL from the kernel;
//   - the stale path is removed BEFORE Listen, because a crashed predecessor leaves its socket file
//     behind and bind would fail with EADDRINUSE against a file nothing is serving;
//   - Chmod runs immediately after Listen and BEFORE the first accept, so the socket is never
//     reachable at the umask-derived permissions.
func listenUnix(socketPath string) (net.Listener, string, error) {
	if runtime.GOOS == "windows" {
		return nil, "", nil
	}
	if limit := unixSocketLimit(); len([]byte(socketPath)) > limit {
		return nil, "", fmt.Errorf("path too long (%d > %d): %s", len([]byte(socketPath)), limit, socketPath)
	}
	if err := ensurePrivateDir(filepath.Dir(socketPath)); err != nil {
		return nil, "", err
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return nil, "", err
	}
	// Bind under a private umask so the inode is never even transiently group/other-connectable. The
	// Chmod below is the guarantee, but between bind and Chmod the mode is 0777 &^ umask — under a
	// permissive umask (0002/0000, seen in CI images) that window is genuinely connectable.
	restore := syscall.Umask(0o077)
	listener, err := net.Listen("unix", socketPath)
	syscall.Umask(restore)
	if err != nil {
		return nil, "", err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		return nil, "", err
	}
	return listener, socketPath, nil
}

// ensurePrivateDir creates the UID-scoped state directory and proves this user exclusively owns it.
//
// MkdirAll returns nil on a directory that ALREADY exists and never re-applies the mode, so on a host
// whose TempDir is the shared /tmp another local user can create $TMPDIR/saki-<uid> first and own it
// permanently. Everything the CLI trusts about the daemon is a file in here — the socket it dials and
// the PID it signals — so an unowned directory is a hijack of the whole channel, not a tidiness issue.
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Lstat, not Stat: a symlink planted at this path satisfies MkdirAll and would redirect every
	// write below it. Under Lstat a symlink is not a directory, so the check below rejects it.
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	// Group/other WRITE is the hijack bit — planting a state file or a socket needs write on the
	// directory. Read/traverse is not the threat: the records inside are 0600, so a 0755 directory
	// (what an inherited umask commonly produces) is safe and must not be rejected.
	if !info.IsDir() || !ok || int(stat.Uid) != os.Getuid() || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("daemon state directory is not private: %s", dir)
	}
	return nil
}

func unixSocketLimit() int {
	if runtime.GOOS == "darwin" {
		return 103
	}
	return 107
}

func writeDaemonState(socketPath, addr string) {
	path := daemonStatePath()
	if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
		log.Printf("daemon state: %v", err)
		return
	}
	socket := any(nil)
	if socketPath != "" {
		socket = socketPath
	}
	body, err := json.Marshal(map[string]any{"pid": os.Getpid(), "socketPath": socket, "goUrl": "http://" + addr})
	if err != nil {
		log.Printf("daemon state: %v", err)
		return
	}
	// O_NOFOLLOW: os.WriteFile follows a symlink at this path and would truncate its target as this
	// user. The state dir check above makes that unreachable; this makes it impossible.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		log.Printf("daemon state: %v", err)
		return
	}
	defer file.Close()
	if _, err := file.Write(body); err != nil {
		log.Printf("daemon state: %v", err)
	}
}

// removeOwnDaemonState deletes the state file only while it still describes THIS process.
//
// An unconditional Remove at shutdown drops whichever record is on disk — including a successor
// daemon's, which leaves a live backend holding the port with nothing tracking its PID. That is the
// orphan outcome 5.3 forbids, and the CLI already guards its own deletes the same way
// (releaseState, src/daemon.ts).
func removeOwnDaemonState() {
	path := daemonStatePath()
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var state struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(body, &state); err != nil || state.PID != os.Getpid() {
		return
	}
	_ = os.Remove(path)
}

// selectProxy decides whether this backend runs STANDALONE or paired with apps/server.
//
// An empty (or whitespace-only) upstream selects NullProxy — every coexistence route degrades to a clean
// refusal instead of dialling a port nothing is listening on. Anything else pairs with apps/server, which
// is what the dev studio and the deployed host both do by pinning SAKI_UPSTREAM explicitly.
//
// Whitespace is treated as unset on purpose: NewHTTPProxy rewrites an empty base to
// "http://localhost:8787" (infra/proxy.go:23-25), so letting " " through would quietly restore the very
// default this function exists to remove.
//
// Returns adapter.Proxy — the SUPERSET interface (it adds StreamEvents). usecase.Proxy is a subset, so the
// same value satisfies NewListService too; Go allows the interface-to-interface assignment.
func selectProxy(upstream string) adapter.Proxy {
	if standalone(upstream) {
		return &infra.NullProxy{}
	}
	// Hand over the TRIMMED value: the standalone check above is trimmed, so deciding on one string and
	// dialling another would build a base URL with stray whitespace. main() already trims, but this
	// keeps the function correct when called on its own (as the tests do).
	return infra.NewHTTPProxy(strings.TrimSpace(upstream))
}

// standalone is the SINGLE predicate for "is there an upstream". selectProxy and proxyMode must both
// use it, or the startup log can describe a mode the process is not actually running in.
func standalone(upstream string) bool { return strings.TrimSpace(upstream) == "" }

// proxyMode is what the startup log prints. Name the MODE, not just the value: a silently-standalone
// backend looks identical to a paired one until a union/passthrough route is hit.
//
// This line is the only cheap way to tell the two modes apart at a glance, which matters because the
// e2e suite genuinely cannot: every e2e run-list assertion is satisfied by Go-owned runs alone, and
// both /events/ reads use Go-OWNED ids, so the whole suite stays green with the upstream absent. It is
// read by a human (or a QA step), not by any committed check — TestProxyMode_AgreesWithSelectProxy is
// what keeps it honest.
func proxyMode(upstream string) string {
	if standalone(upstream) {
		return "standalone (no upstream)"
	}
	return "upstream " + upstream
}

// loopbackAddr binds the IPv4 loopback interface only, so the server is unreachable from any non-loopback
// address (the network-layer half of the privileged-init protection, §10 rule 5).
func loopbackAddr(port string) string { return "127.0.0.1:" + port }
