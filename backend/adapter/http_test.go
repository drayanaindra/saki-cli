package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

// --- fakes for the usecase ports ---

type fakeBranchReader struct{ b *string }

func (f fakeBranchReader) CurrentBranch(string) (*string, error) { return f.b, nil }

type fakeSpawner struct {
	calls      int
	lastKind   domain.RunKind
	lastEngine domain.RunEngine
	err        error
}

func (f *fakeSpawner) Spawn(s usecase.SpawnSpec) (int, func() int, error) {
	f.calls++
	f.lastKind = s.Kind
	f.lastEngine = s.Engine
	if f.err != nil {
		return 0, nil, f.err
	}
	return 4242, func() int { select {} }, nil // wait blocks — run stays running for the test
}

type fakeJournal struct {
	writable bool
	writes   int
}

func (f *fakeJournal) EnsureWritable() error {
	if !f.writable {
		return errors.New("read-only")
	}
	return nil
}
func (f *fakeJournal) Write(domain.Run) error         { f.writes++; return nil }
func (f *fakeJournal) OutPath(id string) string       { return "/tmp/" + id + ".out" }
func (f *fakeJournal) ExitPath(id string) string      { return "/tmp/" + id + ".exit" }
func (f *fakeJournal) AppendOut(string, string) error { return nil }

type fakeStore struct{ runs map[string]domain.Run }

func newFakeStore() *fakeStore                        { return &fakeStore{runs: map[string]domain.Run{}} }
func (s *fakeStore) Put(r domain.Run)                 { s.runs[r.ID] = r }
func (s *fakeStore) Get(id string) (domain.Run, bool) { r, ok := s.runs[id]; return r, ok }
func (s *fakeStore) List() []domain.Run {
	out := []domain.Run{}
	for _, r := range s.runs {
		out = append(out, r)
	}
	return out
}
func (s *fakeStore) SetPid(id string, pid int) {
	if r, ok := s.runs[id]; ok {
		p := pid
		r.Pid = &p
		s.runs[id] = r
	}
}
func (s *fakeStore) Delete(id string) { delete(s.runs, id) }
func (s *fakeStore) Finalize(id string, code int) bool {
	r, ok := s.runs[id]
	if !ok || r.Status != domain.StatusRunning { // first-write-wins (parity)
		return false
	}
	c := code
	r.ExitCode = &c
	r.Status = domain.StatusDone
	if code != 0 {
		r.Status = domain.StatusError
	}
	s.runs[id] = r
	return true
}
func (s *fakeStore) ReserveBuild(laneKey string, run domain.Run) (string, bool) {
	return s.reserveKind("build", laneKey, run)
}
func (s *fakeStore) ReserveInit(laneKey string, run domain.Run) (string, bool) {
	return s.reserveKind("init", laneKey, run)
}
func (s *fakeStore) reserveKind(kind domain.RunKind, laneKey string, run domain.Run) (string, bool) {
	if laneKey != "" {
		for _, r := range s.runs {
			if r.Status == domain.StatusRunning && r.Meta != nil && r.Meta.Kind == kind && r.Meta.LaneKey == laneKey {
				return r.ID, false
			}
		}
	}
	s.runs[run.ID] = run
	return "", true
}
func (s *fakeStore) Update(id string, mut func(r *domain.Run)) bool {
	r, ok := s.runs[id]
	if !ok {
		return false
	}
	mut(&r)
	s.runs[id] = r
	return true
}

// fakeFullOutput implements usecase.FullOutput for the build engine wired into the handler tests. It
// returns no parsed lines by default (a finalized build with no sentinel → retryable/park via caps).
type fakeFullOutput struct{ lines []usecase.ParsedLine }

func (f fakeFullOutput) ReadAll(string) ([]usecase.ParsedLine, error) { return f.lines, nil }
func (fakeFullOutput) Size(string) int64                              { return 0 }

// newEngine builds a BuildEngineService for the handler tests with a no-op Sleeper (auto-resume never
// fires in the HTTP-route tests — the engine's own unit tests exercise the resume chain).
func newEngine(sp usecase.Spawner, j usecase.Journal, st usecase.RunStore, k usecase.ProcessKiller) *usecase.BuildEngineService {
	return usecase.NewBuildEngineService(sp, j, st, fakeClock{}, fakeIDGen{}, k, fakeFullOutput{}, nil, func(int64, func()) {}, nil, usecase.DefaultBuildConfig())
}

type fakeKiller struct {
	signals int
	lastPid int
}

func (k *fakeKiller) SignalGroup(pid int) error { k.signals++; k.lastPid = pid; return nil }
func (k *fakeKiller) KillGroup(pid int) error   { k.signals++; k.lastPid = pid; return nil }
func (k *fakeKiller) Alive(int) bool            { return false }

type fakeProxy struct {
	status       int
	body         []byte
	forwards     int
	lastMethod   string
	lastPath     string
	runs         []map[string]any
	streamCalls  int
	lastStreamID string
}

func (f *fakeProxy) Forward(m, p, c string, b []byte) (int, []byte, error) {
	f.forwards++
	f.lastMethod, f.lastPath = m, p
	return f.status, f.body, nil
}
func (f *fakeProxy) GetRuns(string) ([]map[string]any, error) { return f.runs, nil }
func (f *fakeProxy) StreamEvents(_ context.Context, id, _ string, w http.ResponseWriter) {
	f.streamCalls++
	f.lastStreamID = id
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("data: passthrough\n\n"))
}

// fakeOutput serves the run's .out bytes once, then EOF (empty) — enough for a finalized-run tail.
type fakeOutput struct {
	data   string
	served bool
}

func (f *fakeOutput) ReadFrom(_ string, offset int64) ([]byte, int64, error) {
	if f.served {
		return nil, offset, nil
	}
	f.served = true
	return []byte(f.data), int64(len(f.data)), nil
}

func intp(n int) *int { return &n }

type fakeClock struct{}

func (fakeClock) Now() int64 { return 1000 }

type fakeIDGen struct{}

func (fakeIDGen) New() string { return "run-1" }

// fakeGitWriter is the usecase.GitWriter port fake for the git-write handler tests.
type fakeGitWriter struct {
	branches  []string
	switchOK  bool
	switchErr string
	remotes   []string
	defBranch string
	curBranch string
	mrURL     string
	mrErr     string
	mergeOK   bool
	mergeErr  string
	mergeBr   string
}

func (f fakeGitWriter) ListBranches(string) ([]string, error) { return f.branches, nil }
func (f fakeGitWriter) Switch(_, _ string, _ bool) (bool, string) {
	return f.switchOK, f.switchErr
}
func (f fakeGitWriter) Remotes(string) []string         { return f.remotes }
func (f fakeGitWriter) DefaultBranch(string) string     { return f.defBranch }
func (f fakeGitWriter) CurrentBranchName(string) string { return f.curBranch }
func (f fakeGitWriter) CreateMR(_, _, _ string) (string, string) {
	return f.mrURL, f.mrErr
}
func (f fakeGitWriter) Merge(_, _ string) (bool, string) { return f.mergeOK, f.mergeErr }
func (f fakeGitWriter) MergeAbort(string)                {}

func gitWriteSvc(w fakeGitWriter) usecase.GitWriteService { return usecase.NewGitWriteService(w) }

// fakeContentFS is an in-memory RoadmapFS + WorkItemsFS for the F4 content-route tests. files maps
// an absolute path → content; walk lists the .md paths WalkMarkdown should return.
type fakeContentFS struct {
	files   map[string]string
	walk    []string
	mtimes  map[string]int64
	dirList map[string][]string
}

func (f fakeContentFS) Exists(p string) bool         { _, ok := f.files[p]; return ok }
func (f fakeContentFS) Read(p string) (string, bool) { s, ok := f.files[p]; return s, ok }
func (f fakeContentFS) MTimeMs(p string) int64       { return f.mtimes[p] }
func (f fakeContentFS) ListDir(d string) []string    { return f.dirList[d] }
func (f fakeContentFS) WalkMarkdown(string) []string { return f.walk }

// fakeCommitVerifier: no commit ever resolves (parity with a fixture repo lacking the SHA).
type fakeCommitVerifier struct{}

func (fakeCommitVerifier) CommitExists(string, string) bool { return false }

func roadmapSvc(fs usecase.RoadmapFS) usecase.RoadmapService { return usecase.NewRoadmapService(fs) }
func workitemsSvc(fs usecase.WorkItemsFS) usecase.WorkItemsService {
	return usecase.NewWorkItemsService(fs, fakeCommitVerifier{})
}

// emptyContent wires content services whose fs is empty — for handlers whose tests never hit the
// content routes.
func emptyRoadmap() usecase.RoadmapService     { return roadmapSvc(fakeContentFS{}) }
func emptyWorkitems() usecase.WorkItemsService { return workitemsSvc(fakeContentFS{}) }

func prdSvc(fs usecase.WorkItemsFS) usecase.PrdService { return usecase.NewPrdService(fs) }
func emptyPrd() usecase.PrdService                     { return prdSvc(fakeContentFS{}) }

// fakeContentWriter records lock writes (or returns err to exercise the R6 graceful branch).
type fakeContentWriter struct {
	writes map[string]string
	err    error
}

func (f *fakeContentWriter) WriteFile(path, content string) error {
	if f.err != nil {
		return f.err
	}
	if f.writes != nil {
		f.writes[path] = content
	}
	return nil
}

// fakeGitUser stubs `git config user.name` for the approver fallback.
type fakeGitUser struct{ name string }

func (f fakeGitUser) UserName(string) string { return f.name }

func lockSvc(fs usecase.WorkItemsFS, w usecase.ContentWriter, g usecase.GitUserReader) usecase.LockService {
	return usecase.NewLockService(fs, w, g)
}
func emptyLock() usecase.LockService {
	return lockSvc(fakeContentFS{}, &fakeContentWriter{}, fakeGitUser{})
}

// fakeGitSlice stubs the per-slice feat-commit lookup.
type fakeGitSlice struct{ commit string }

func (f fakeGitSlice) SliceCommit(_, _ string) string { return f.commit }

func blockersSvc(fs usecase.WorkItemsFS) usecase.BlockersService {
	return usecase.NewBlockersService(fs)
}
func emptyBlockers() usecase.BlockersService { return blockersSvc(fakeContentFS{}) }

// emptyDoctor wraps a harmless fakeEngineProofs (doctor_http_test.go) that always reports ok —
// matching every sibling emptyX() helper's convention of wrapping a real fake rather than a bare
// zero-value. A bare usecase.DoctorService{} (nil EngineProofs) would nil-panic the moment any test
// file below is later extended to exercise /api/doctor; this can never panic.
func emptyDoctor() usecase.DoctorService { return usecase.NewDoctorService(&fakeEngineProofs{}) }

// noopProvisioner satisfies usecase.EngineProvisioner without touching the filesystem or spawning a
// child — the safe default for the many handlers below that never exercise /api/init-env.
type noopProvisioner struct{}

func (noopProvisioner) Provision(usecase.ProvisionRequest) (bool, error) { return false, nil }

// emptyInitEnv is the init-env twin of emptyDoctor, and exists for the same reason: a bare
// usecase.InitEnvService{} has nil ports, and POST /api/init-env is registered unconditionally, so
// the zero value would nil-panic the moment any test file here is extended to exercise that route.
func emptyInitEnv() usecase.InitEnvService {
	return usecase.NewInitEnvService(noopProvisioner{}, &fakeEngineProofs{})
}

func sliceMetaSvcFor(g usecase.GitSliceReader) usecase.SliceMetaService {
	return usecase.NewSliceMetaService(g)
}
func emptySliceMeta() usecase.SliceMetaService { return sliceMetaSvcFor(fakeGitSlice{}) }

func resolveSvc(fs usecase.WorkItemsFS, w usecase.ContentWriter) usecase.ResolveBlockerService {
	return usecase.NewResolveBlockerService(fs, w)
}
func emptyResolve() usecase.ResolveBlockerService {
	return resolveSvc(fakeContentFS{}, &fakeContentWriter{})
}

func planTrackSvc(fs usecase.RoadmapFS, w usecase.ContentWriter) usecase.PlanTrackService {
	return usecase.NewPlanTrackService(fs, w)
}
func emptyPlanTrack() usecase.PlanTrackService {
	return planTrackSvc(fakeContentFS{}, &fakeContentWriter{})
}

// px is the Proxy INTERFACE, not *fakeProxy, so these helpers can also wire the real infra.NullProxy
// (I8) and prove the standalone refusals through the real mux. Every existing &fakeProxy{} call site
// still satisfies it.
func buildHandler(branch *string, sp *fakeSpawner, j *fakeJournal, st *fakeStore, px Proxy) Handler {
	bs := usecase.NewBranchService(fakeBranchReader{b: branch})
	rs := usecase.NewRunService(sp, j, st, fakeClock{}, fakeIDGen{})
	eng := newEngine(sp, j, st, &fakeKiller{})
	ls := usecase.NewListService(st, px, fakeFullOutput{})
	ss := usecase.NewStreamService(st, &fakeOutput{}, fakeClock{}, time.Millisecond)
	sk := usecase.NewStopService(st, &fakeKiller{})
	return NewHandler(bs, rs, eng, ls, ss, sk, px, gitWriteSvc(fakeGitWriter{}), emptyRoadmap(), emptyWorkitems(), emptyPrd(), emptyLock(), emptyBlockers(), emptySliceMeta(), emptyResolve(), emptyPlanTrack(), emptyDoctor(), emptyInitEnv())
}

// buildStreamHandler wires a specific run output for the /events tail tests.
func buildStreamHandler(st *fakeStore, out usecase.RunOutput, px Proxy) Handler {
	rs := usecase.NewRunService(&fakeSpawner{}, &fakeJournal{writable: true}, st, fakeClock{}, fakeIDGen{})
	eng := newEngine(&fakeSpawner{}, &fakeJournal{writable: true}, st, &fakeKiller{})
	ss := usecase.NewStreamService(st, out, fakeClock{}, time.Millisecond)
	sk := usecase.NewStopService(st, &fakeKiller{})
	return NewHandler(usecase.NewBranchService(fakeBranchReader{}), rs, eng, usecase.NewListService(st, px, fakeFullOutput{}), ss, sk, px, gitWriteSvc(fakeGitWriter{}), emptyRoadmap(), emptyWorkitems(), emptyPrd(), emptyLock(), emptyBlockers(), emptySliceMeta(), emptyResolve(), emptyPlanTrack(), emptyDoctor(), emptyInitEnv())
}

// buildStopHandler exposes the killer for the /api/run/:id/stop tests.
func buildStopHandler(st *fakeStore, k *fakeKiller, px Proxy) Handler {
	rs := usecase.NewRunService(&fakeSpawner{}, &fakeJournal{writable: true}, st, fakeClock{}, fakeIDGen{})
	eng := newEngine(&fakeSpawner{}, &fakeJournal{writable: true}, st, k)
	ss := usecase.NewStreamService(st, &fakeOutput{}, fakeClock{}, time.Millisecond)
	sk := usecase.NewStopService(st, k)
	return NewHandler(usecase.NewBranchService(fakeBranchReader{}), rs, eng, usecase.NewListService(st, px, fakeFullOutput{}), ss, sk, px, gitWriteSvc(fakeGitWriter{}), emptyRoadmap(), emptyWorkitems(), emptyPrd(), emptyLock(), emptyBlockers(), emptySliceMeta(), emptyResolve(), emptyPlanTrack(), emptyDoctor(), emptyInitEnv())
}

func post(t *testing.T, srv *httptest.Server, path, body string) *http.Response {
	t.Helper()
	res, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// --- health (liveness) ---

// The probe must answer from a Handler with NO services wired: `saki status` calls it to decide
// whether this process is listening at all, which has to be answerable before any usecase is.
func TestHealthHandler(t *testing.T) {
	srv := httptest.NewServer(Handler{}.Routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body struct {
		Ok      bool
		Service string
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Ok {
		t.Fatalf("ok %v", body.Ok)
	}
	// Distinct from apps/server's "pipeline-studio-server" — same name on both origins would let a
	// status probe that hit Express twice look like it had reached both servers.
	if body.Service != "saki-backend" {
		t.Fatalf("service %q", body.Service)
	}
}

// --- branch (slice 1, kept green under the new signature) ---

func TestBranchHandler(t *testing.T) {
	name := "main"
	srv := httptest.NewServer(buildHandler(&name, &fakeSpawner{}, &fakeJournal{writable: true}, newFakeStore(), &fakeProxy{}).Routes())
	defer srv.Close()

	res, _ := http.Get(srv.URL + "/api/branch?cwd=/x")
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body struct{ Branch *string }
	json.NewDecoder(res.Body).Decode(&body)
	if body.Branch == nil || *body.Branch != "main" {
		t.Fatalf("branch %v", body.Branch)
	}

	res2, _ := http.Get(srv.URL + "/api/branch")
	if res2.StatusCode != 422 {
		t.Fatalf("no-cwd status %d", res2.StatusCode)
	}
}

// --- F3 slice 1: git-write (branch list + switch), behind the loopback OriginGuard ---

func buildGitHandler(w fakeGitWriter) Handler {
	st := newFakeStore()
	px := &fakeProxy{}
	rs := usecase.NewRunService(&fakeSpawner{}, &fakeJournal{writable: true}, st, fakeClock{}, fakeIDGen{})
	eng := newEngine(&fakeSpawner{}, &fakeJournal{writable: true}, st, &fakeKiller{})
	ss := usecase.NewStreamService(st, &fakeOutput{}, fakeClock{}, time.Millisecond)
	sk := usecase.NewStopService(st, &fakeKiller{})
	return NewHandler(usecase.NewBranchService(fakeBranchReader{}), rs, eng, usecase.NewListService(st, px, fakeFullOutput{}), ss, sk, px, gitWriteSvc(w), emptyRoadmap(), emptyWorkitems(), emptyPrd(), emptyLock(), emptyBlockers(), emptySliceMeta(), emptyResolve(), emptyPlanTrack(), emptyDoctor(), emptyInitEnv())
}

func TestBranchesHandler(t *testing.T) {
	srv := httptest.NewServer(buildGitHandler(fakeGitWriter{branches: []string{"main", "dev"}}).Routes())
	defer srv.Close()

	res, _ := http.Get(srv.URL + "/api/branches?cwd=/r")
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body struct{ Branches []string }
	json.NewDecoder(res.Body).Decode(&body)
	if len(body.Branches) != 2 || body.Branches[0] != "main" {
		t.Fatalf("branches %v", body.Branches)
	}
	if res2, _ := http.Get(srv.URL + "/api/branches"); res2.StatusCode != 422 {
		t.Fatalf("no-cwd status %d", res2.StatusCode)
	}
}

func TestSwitchBranchHandler(t *testing.T) {
	srv := httptest.NewServer(buildGitHandler(fakeGitWriter{switchOK: true}).Routes())
	defer srv.Close()

	res := post(t, srv, "/api/switch-branch", `{"cwd":"/r","branch":"feat"}`)
	if res.StatusCode != 200 {
		t.Fatalf("happy status %d", res.StatusCode)
	}
	var ok struct {
		OK     bool
		Branch string
	}
	json.NewDecoder(res.Body).Decode(&ok)
	if !ok.OK || ok.Branch != "feat" {
		t.Fatalf("happy body %+v", ok)
	}
	if r := post(t, srv, "/api/switch-branch", `{"cwd":"/r"}`); r.StatusCode != 422 {
		t.Fatalf("missing-branch status %d", r.StatusCode)
	}
	if r := post(t, srv, "/api/switch-branch", `{"cwd":"/r","branch":"-rf","create":true}`); r.StatusCode != 422 {
		t.Fatalf("BR5 invalid-name status %d", r.StatusCode)
	}

	srv2 := httptest.NewServer(buildGitHandler(fakeGitWriter{switchOK: false, switchErr: "would be overwritten"}).Routes())
	defer srv2.Close()
	r4 := post(t, srv2, "/api/switch-branch", `{"cwd":"/r","branch":"feat"}`)
	if r4.StatusCode != 200 {
		t.Fatalf("graceful status %d", r4.StatusCode)
	}
	var gf struct {
		OK    bool
		Error string
	}
	json.NewDecoder(r4.Body).Decode(&gf)
	if gf.OK || gf.Error != "would be overwritten" {
		t.Fatalf("graceful body %+v", gf)
	}
}

func TestCreateMRHandler(t *testing.T) {
	// happy: {ok:true,url}
	srv := httptest.NewServer(buildGitHandler(fakeGitWriter{remotes: []string{"origin"}, defBranch: "main", curBranch: "feat", mrURL: "https://gl/mr/1"}).Routes())
	defer srv.Close()
	res := post(t, srv, "/api/create-mr", `{"cwd":"/r"}`)
	if res.StatusCode != 200 {
		t.Fatalf("happy status %d", res.StatusCode)
	}
	var ok struct {
		OK  bool
		URL string
	}
	json.NewDecoder(res.Body).Decode(&ok)
	if !ok.OK || ok.URL != "https://gl/mr/1" {
		t.Fatalf("happy body %+v", ok)
	}

	// graceful no-remote → {ok:false,error} at 200
	srv2 := httptest.NewServer(buildGitHandler(fakeGitWriter{remotes: nil, curBranch: "feat"}).Routes())
	defer srv2.Close()
	r2 := post(t, srv2, "/api/create-mr", `{"cwd":"/r"}`)
	if r2.StatusCode != 200 {
		t.Fatalf("no-remote status %d", r2.StatusCode)
	}
	var gf struct {
		OK    bool
		Error string
	}
	json.NewDecoder(r2.Body).Decode(&gf)
	if gf.OK || gf.Error != "no git remote configured — add a remote before creating an MR" {
		t.Fatalf("no-remote body %+v", gf)
	}

	// missing cwd → 422
	if r := post(t, srv, "/api/create-mr", `{}`); r.StatusCode != 422 {
		t.Fatalf("missing-cwd status %d", r.StatusCode)
	}
}

func TestMergeToMainHandler(t *testing.T) {
	srv := httptest.NewServer(buildGitHandler(fakeGitWriter{curBranch: "feat", branches: []string{"main", "feat"}, switchOK: true, mergeOK: true}).Routes())
	defer srv.Close()
	res := post(t, srv, "/api/merge-to-main", `{"cwd":"/r"}`)
	if res.StatusCode != 200 {
		t.Fatalf("happy status %d", res.StatusCode)
	}
	var ok struct {
		OK             bool
		Branch, Source string
	}
	json.NewDecoder(res.Body).Decode(&ok)
	if !ok.OK || ok.Branch != "main" || ok.Source != "feat" {
		t.Fatalf("happy body %+v", ok)
	}

	// graceful conflict → {ok:false,error} at 200
	srv2 := httptest.NewServer(buildGitHandler(fakeGitWriter{curBranch: "feat", branches: []string{"main", "feat"}, switchOK: true, mergeOK: false, mergeErr: "CONFLICT (content)"}).Routes())
	defer srv2.Close()
	r2 := post(t, srv2, "/api/merge-to-main", `{"cwd":"/r"}`)
	if r2.StatusCode != 200 {
		t.Fatalf("conflict status %d", r2.StatusCode)
	}
	var gf struct {
		OK    bool
		Error string
	}
	json.NewDecoder(r2.Body).Decode(&gf)
	if gf.OK || gf.Error != "CONFLICT (content)" {
		t.Fatalf("conflict body %+v", gf)
	}

	if r := post(t, srv, "/api/merge-to-main", `{}`); r.StatusCode != 422 {
		t.Fatalf("missing-cwd status %d", r.StatusCode)
	}
}

// BR7 scoping: the git-write routes are origin-guarded; the run-vertical routes are NOT.
func TestGitWriteRoutes_originGuardScoped(t *testing.T) {
	mux := buildGitHandler(fakeGitWriter{branches: []string{"main"}}).Routes()

	req := httptest.NewRequest(http.MethodGet, "/api/branches?cwd=/r", nil)
	req.Host = "evil.com"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("git-write route want 403 on non-loopback host, got %d", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/branch?cwd=/r", nil)
	req2.Host = "evil.com"
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code == http.StatusForbidden {
		t.Fatal("run-vertical /api/branch must NOT be origin-guarded")
	}
}

// --- slice 2 ---

func TestPostRun_NonBuildSpawns(t *testing.T) {
	sp, j, st := &fakeSpawner{}, &fakeJournal{writable: true}, newFakeStore()
	srv := httptest.NewServer(buildHandler(nil, sp, j, st, &fakeProxy{}).Routes())
	defer srv.Close()

	res := post(t, srv, "/api/run", `{"prompt":"say hi","meta":{"kind":"generate","laneKey":"g1"}}`)
	if res.StatusCode != 201 {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body struct{ RunId string }
	json.NewDecoder(res.Body).Decode(&body)
	if body.RunId != "run-1" {
		t.Fatalf("runId %q", body.RunId)
	}
	if sp.calls != 1 || j.writes < 1 {
		t.Fatalf("spawner calls %d, journal writes %d", sp.calls, j.writes)
	}
	if _, ok := st.Get("run-1"); !ok {
		t.Fatal("run not stored")
	}
}

// AC 1.1 — a build POST now spawns LOCALLY on Go (was forwarded to apps/server before F6). It is not
// reverse-proxied; the Go spawner is invoked with kind:"build".
func TestPostRun_BuildSpawnsLocally(t *testing.T) {
	sp := &fakeSpawner{}
	px := &fakeProxy{}
	srv := httptest.NewServer(buildHandler(nil, sp, &fakeJournal{writable: true}, newFakeStore(), px).Routes())
	defer srv.Close()

	res := post(t, srv, "/api/run", `{"prompt":"/saki-builder:build F2","meta":{"kind":"build","laneKey":"b1"}}`)
	if res.StatusCode != 201 {
		t.Fatalf("status %d, want 201 (local spawn)", res.StatusCode)
	}
	var body struct{ RunId string }
	json.NewDecoder(res.Body).Decode(&body)
	if body.RunId != "run-1" {
		t.Fatalf("runId %q", body.RunId)
	}
	if sp.calls != 1 || sp.lastKind != "build" {
		t.Fatalf("build must spawn locally with kind build: calls=%d kind=%q", sp.calls, sp.lastKind)
	}
	if px.forwards != 0 {
		t.Fatal("build must NOT reverse-proxy (it is Go-owned as of F6)")
	}
}

// AC 4.1 — F7 · P6 slice 4: init now spawns LOCALLY on Go (with --dangerously-skip-permissions, keyed
// in the spawner on kind:init) and is NOT reverse-proxied to apps/server.
func TestPostRun_InitSpawnsLocallyNotForwarded(t *testing.T) {
	sp := &fakeSpawner{}
	px := &fakeProxy{}
	srv := httptest.NewServer(buildHandler(nil, sp, &fakeJournal{writable: true}, newFakeStore(), px).Routes())
	defer srv.Close()
	res := post(t, srv, "/api/run", `{"prompt":"/init-env","meta":{"kind":"init","laneKey":"/r/prd.md"}}`)
	if res.StatusCode != 201 {
		t.Fatalf("status %d, want 201 (local privileged spawn)", res.StatusCode)
	}
	if sp.calls != 1 || sp.lastKind != "init" {
		t.Fatalf("init must spawn locally with kind init: calls=%d kind=%q", sp.calls, sp.lastKind)
	}
	if px.forwards != 0 {
		t.Fatalf("init must NOT reverse-proxy to apps/server (Go-owned now): forwards=%d", px.forwards)
	}
}

// AC 4.3 — a second identical init POST re-adopts the in-flight run (200 deduped) and spawns NO second
// privileged process.
func TestPostRun_InitDedupe(t *testing.T) {
	sp := &fakeSpawner{}
	srv := httptest.NewServer(buildHandler(nil, sp, &fakeJournal{writable: true}, newFakeStore(), &fakeProxy{}).Routes())
	defer srv.Close()
	body := `{"prompt":"/init-env","meta":{"kind":"init","laneKey":"/r/prd.md"}}`
	if r := post(t, srv, "/api/run", body); r.StatusCode != 201 {
		t.Fatalf("first init status %d, want 201", r.StatusCode)
	}
	res := post(t, srv, "/api/run", body)
	if res.StatusCode != 200 {
		t.Fatalf("second identical init status %d, want 200 (deduped)", res.StatusCode)
	}
	var body2 struct {
		RunId   string `json:"runId"`
		Deduped bool   `json:"deduped"`
	}
	json.NewDecoder(res.Body).Decode(&body2)
	if !body2.Deduped || body2.RunId != "run-1" {
		t.Fatalf("dedupe body %+v, want the in-flight runId with deduped:true", body2)
	}
	if sp.calls != 1 {
		t.Fatalf("dedupe must spawn exactly ONE privileged process, got %d", sp.calls)
	}
}

// AC 4.2 defense-in-depth: even behind the loopback bind, a cross-origin/DNS-rebind init POST from the
// operator's OWN browser is 403'd (localhost-CSRF) and never reaches the privileged spawn.
func TestPostRun_InitCrossOriginForbidden(t *testing.T) {
	sp := &fakeSpawner{}
	mux := buildHandler(nil, sp, &fakeJournal{writable: true}, newFakeStore(), &fakeProxy{}).Routes()
	req := httptest.NewRequest(http.MethodPost, "/api/run",
		strings.NewReader(`{"prompt":"/init-env","meta":{"kind":"init","laneKey":"/r/prd.md"}}`))
	req.Host = "localhost:5180"
	req.Header.Set("Origin", "http://evil.com")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin init status %d, want 403", rec.Code)
	}
	if sp.calls != 0 {
		t.Fatal("a cross-origin init must NEVER reach the privileged spawn")
	}
}

// A non-init kind on /api/run stays UNGUARDED (Non-Goal: don't change build/generate behavior) — a
// generate POST with a cross-origin Origin still spawns.
func TestPostRun_NonInitNotOriginGuarded(t *testing.T) {
	sp := &fakeSpawner{}
	mux := buildHandler(nil, sp, &fakeJournal{writable: true}, newFakeStore(), &fakeProxy{}).Routes()
	req := httptest.NewRequest(http.MethodPost, "/api/run",
		strings.NewReader(`{"prompt":"say hi","meta":{"kind":"generate","laneKey":"g1"}}`))
	req.Host = "localhost:5180"
	req.Header.Set("Origin", "http://evil.com")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatal("a non-init run POST must NOT be origin-guarded (only init is)")
	}
}

func TestPostRun_NoPrompt422(t *testing.T) {
	sp := &fakeSpawner{}
	srv := httptest.NewServer(buildHandler(nil, sp, &fakeJournal{writable: true}, newFakeStore(), &fakeProxy{}).Routes())
	defer srv.Close()
	for _, body := range []string{`{}`, `{"prompt":123}`} {
		res := post(t, srv, "/api/run", body)
		if res.StatusCode != 422 {
			t.Fatalf("body %s → status %d, want 422", body, res.StatusCode)
		}
	}
	if sp.calls != 0 {
		t.Fatal("no spawn on invalid prompt")
	}
}

func TestPostRun_UnwritableDir500(t *testing.T) {
	sp := &fakeSpawner{}
	srv := httptest.NewServer(buildHandler(nil, sp, &fakeJournal{writable: false}, newFakeStore(), &fakeProxy{}).Routes())
	defer srv.Close()
	res := post(t, srv, "/api/run", `{"prompt":"hi","meta":{"kind":"generate","laneKey":"g"}}`)
	if res.StatusCode != 500 {
		t.Fatalf("status %d, want 500", res.StatusCode)
	}
	if sp.calls != 0 {
		t.Fatal("no phantom spawn when the runs dir is unwritable")
	}
}

func TestGetRuns_Union(t *testing.T) {
	st := newFakeStore()
	local := "/repo"
	st.Put(domain.Run{ID: "local-1", Status: "running", StartedAt: 2000, Cwd: &local, Meta: &domain.Meta{Kind: "generate", LaneKey: "g"}})
	px := &fakeProxy{runs: []map[string]any{
		{"id": "up-1", "status": "running", "startedAt": float64(3000)}, // newer, from apps/server
		{"id": "local-1", "status": "stale", "startedAt": float64(1)},   // dup id → local wins, dropped
	}}
	srv := httptest.NewServer(buildHandler(nil, &fakeSpawner{}, &fakeJournal{writable: true}, st, px).Routes())
	defer srv.Close()

	res, _ := http.Get(srv.URL + "/api/runs")
	var runs []map[string]any
	json.NewDecoder(res.Body).Decode(&runs)
	if len(runs) != 2 {
		t.Fatalf("want 2 (union, dedup), got %d", len(runs))
	}
	if runs[0]["id"] != "up-1" { // newest-first
		t.Fatalf("want up-1 first (newest), got %v", runs[0]["id"])
	}
	// the local copy of local-1 (status running) wins over the upstream dup (status stale)
	for _, r := range runs {
		if r["id"] == "local-1" && r["status"] != "running" {
			t.Fatalf("local should win dedup, got status %v", r["status"])
		}
	}
}

func TestStopRun_OwnedSignalsGroup(t *testing.T) {
	st := newFakeStore()
	pid := 4242
	st.Put(domain.Run{ID: "r1", Status: domain.StatusRunning, Pid: &pid})
	k := &fakeKiller{}
	srv := httptest.NewServer(buildStopHandler(st, k, &fakeProxy{}).Routes())
	defer srv.Close()

	res := post(t, srv, "/api/run/r1/stop", "")
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if k.signals != 1 || k.lastPid != 4242 {
		t.Fatalf("process group not signalled: %+v", k)
	}
	if r, _ := st.Get("r1"); r.Status != domain.StatusError {
		t.Fatalf("stopped run must be errored, got %s", r.Status)
	}
}

func TestStopRun_FinishedRun404(t *testing.T) {
	st := newFakeStore()
	st.Put(domain.Run{ID: "r1", Status: domain.StatusDone})
	srv := httptest.NewServer(buildStopHandler(st, &fakeKiller{}, &fakeProxy{}).Routes())
	defer srv.Close()
	res := post(t, srv, "/api/run/r1/stop", "")
	if res.StatusCode != 404 {
		t.Fatalf("stop of a finished run → want 404, got %d", res.StatusCode)
	}
}

func TestStopRun_UnownedForwarded(t *testing.T) {
	px := &fakeProxy{status: 200, body: []byte(`{"stopped":true}`)}
	srv := httptest.NewServer(buildStopHandler(newFakeStore(), &fakeKiller{}, px).Routes())
	defer srv.Close()
	res := post(t, srv, "/api/run/build-x/stop", "")
	if res.StatusCode != 200 || px.forwards != 1 || !strings.Contains(px.lastPath, "/api/run/build-x/stop") {
		t.Fatalf("un-owned stop must forward to apps/server: status=%d path=%s", res.StatusCode, px.lastPath)
	}
}

func TestEvents_TailsOwnedRun(t *testing.T) {
	st := newFakeStore()
	st.Put(domain.Run{ID: "r1", Status: domain.StatusDone, ExitCode: intp(0)}) // finalized → drains + ends
	out := &fakeOutput{data: "{\"type\":\"a\"}\n{\"type\":\"b\"}\n"}
	srv := httptest.NewServer(buildStreamHandler(st, out, &fakeProxy{}).Routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/events/r1")
	if err != nil {
		t.Fatal(err)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	body, _ := io.ReadAll(res.Body)
	s := string(body)
	if strings.Count(s, "data: ") < 3 { // 2 events + the end frame's data line
		t.Fatalf("want ≥3 data frames, got:\n%s", s)
	}
	if !strings.Contains(s, `"kind":"json"`) || !strings.Contains(s, `event: end`) {
		t.Fatalf("missing json event or end frame:\n%s", s)
	}
	if !strings.Contains(s, `"status":"done"`) {
		t.Fatalf("end frame missing status:\n%s", s)
	}
}

func TestEvents_PassthroughForUnownedID(t *testing.T) {
	px := &fakeProxy{}
	srv := httptest.NewServer(buildStreamHandler(newFakeStore(), &fakeOutput{}, px).Routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/events/build-xyz") // not in the store → passthrough
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	if px.streamCalls != 1 || px.lastStreamID != "build-xyz" {
		t.Fatalf("un-owned id must passthrough: calls=%d id=%s", px.streamCalls, px.lastStreamID)
	}
	if !strings.Contains(string(body), "passthrough") {
		t.Fatalf("passthrough body not relayed: %s", body)
	}
}

func TestForwardHostedPostRuns(t *testing.T) {
	px := &fakeProxy{status: 201, body: []byte(`{"runId":"sandbox-1"}`)}
	srv := httptest.NewServer(buildHandler(nil, &fakeSpawner{}, &fakeJournal{writable: true}, newFakeStore(), px).Routes())
	defer srv.Close()
	res := post(t, srv, "/api/runs", `{"machineId":"m1"}`)
	if res.StatusCode != 201 || px.lastPath != "/api/runs" {
		t.Fatalf("hosted POST /api/runs not forwarded: status %d path %s", res.StatusCode, px.lastPath)
	}
}

// E26 — the engine boundary validator accepts only the known runtimes (empty = claude default).
func TestParseRunEngine(t *testing.T) {
	tests := []struct {
		in   domain.RunEngine
		want domain.RunEngine
		ok   bool
	}{
		{"", domain.EngineClaude, true},
		{"claude", domain.EngineClaude, true},
		{"opencode", domain.EngineOpencode, true},
		{"codex", domain.EngineCodex, true},
		{"omp", domain.EngineOMP, true},
		{"bogus", "", false},
		{"CLAUDE", "", false},
		{"CODEX", "", false},
	}
	for _, tc := range tests {
		got, err := parseRunEngine(tc.in)
		if tc.ok && (err != nil || got != tc.want) {
			t.Fatalf("parseRunEngine(%q) = %q, %v; want %q, nil", tc.in, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Fatalf("parseRunEngine(%q) must reject", tc.in)
		}
	}
}

// E26 9.5.1 — POST /api/run with engine=opencode threads it to the SpawnSpec AND the stored run record
// (the operator's selection surfaces on the run, end to end through the HTTP seam).
func TestPostRun_EngineThreadedToSpawnAndRecord(t *testing.T) {
	sp, j, st := &fakeSpawner{}, &fakeJournal{writable: true}, newFakeStore()
	srv := httptest.NewServer(buildHandler(nil, sp, j, st, &fakeProxy{}).Routes())
	defer srv.Close()

	res := post(t, srv, "/api/run", `{"prompt":"/build","meta":{"kind":"build","laneKey":"b1"},"engine":"opencode"}`)
	if res.StatusCode != 201 {
		t.Fatalf("status %d, want 201", res.StatusCode)
	}
	if sp.lastEngine != domain.EngineOpencode {
		t.Fatalf("the SpawnSpec must carry the opencode engine, got %q", sp.lastEngine)
	}
	run, ok := st.Get("run-1")
	if !ok {
		t.Fatal("run not stored")
	}
	if run.Engine != domain.EngineOpencode {
		t.Fatalf("the stored run record must carry engine=opencode, got %q", run.Engine)
	}
}

// The same seam for codex — the third runtime threads through unchanged, which is the point of keying
// on the enum rather than on an `is-opencode` boolean.
func TestPostRun_CodexEngineThreadedToSpawnAndRecord(t *testing.T) {
	sp, j, st := &fakeSpawner{}, &fakeJournal{writable: true}, newFakeStore()
	srv := httptest.NewServer(buildHandler(nil, sp, j, st, &fakeProxy{}).Routes())
	defer srv.Close()

	res := post(t, srv, "/api/run", `{"prompt":"/saki-builder:build tasks/prd-x.md","meta":{"kind":"build","laneKey":"b1"},"engine":"codex"}`)
	if res.StatusCode != 201 {
		t.Fatalf("status %d, want 201", res.StatusCode)
	}
	if sp.lastEngine != domain.EngineCodex {
		t.Fatalf("the SpawnSpec must carry the codex engine, got %q", sp.lastEngine)
	}
	run, ok := st.Get("run-1")
	if !ok {
		t.Fatal("run not stored")
	}
	if run.Engine != domain.EngineCodex {
		t.Fatalf("the stored run record must carry engine=codex, got %q", run.Engine)
	}
}

func TestPostRun_OMPEngineThreadedToSpawnAndRecord(t *testing.T) {
	sp, j, st := &fakeSpawner{}, &fakeJournal{writable: true}, newFakeStore()
	srv := httptest.NewServer(buildHandler(nil, sp, j, st, &fakeProxy{}).Routes())
	defer srv.Close()

	res := post(t, srv, "/api/run", `{"prompt":"/saki-builder:build tasks/prd-x.md","meta":{"kind":"build","laneKey":"b1"},"engine":"omp"}`)
	if res.StatusCode != 201 {
		t.Fatalf("status %d, want 201", res.StatusCode)
	}
	if sp.lastEngine != domain.EngineOMP {
		t.Fatalf("the SpawnSpec must carry the omp engine, got %q", sp.lastEngine)
	}
	run, ok := st.Get("run-1")
	if !ok {
		t.Fatal("run not stored")
	}
	if run.Engine != domain.EngineOMP {
		t.Fatalf("the stored run record must carry engine=omp, got %q", run.Engine)
	}
}

// A not-provisioned engine profile must surface its DIAGNOSIS, not a generic "spawn failed". The whole
// value of the pre-spawn proofs is that the operator learns why a run was refused (which profile, and
// the installer to run); swallowing the message would leave a loud refusal indistinguishable from any
// other 500.
func TestPostRun_EngineNotProvisionedSurfacesTheReason(t *testing.T) {
	sp := &fakeSpawner{err: errors.Join(usecase.ErrEngineNotProvisioned, errors.New("run scripts/install-codex-skills.sh"))}
	srv := httptest.NewServer(buildHandler(nil, sp, &fakeJournal{writable: true}, newFakeStore(), &fakeProxy{}).Routes())
	defer srv.Close()

	res := post(t, srv, "/api/run", `{"prompt":"/saki-builder:build x","meta":{"kind":"generate","laneKey":"l1"},"engine":"codex"}`)
	if res.StatusCode != 500 {
		t.Fatalf("status %d, want 500", res.StatusCode)
	}
	b, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(b), "install-codex-skills.sh") {
		t.Fatalf("the refusal reason must reach the operator, got %s", b)
	}
}

// E26 9.5.1 — an omitted engine stays the claude default (today's behavior).
func TestPostRun_EngineDefaultsToClaude(t *testing.T) {
	sp := &fakeSpawner{}
	srv := httptest.NewServer(buildHandler(nil, sp, &fakeJournal{writable: true}, newFakeStore(), &fakeProxy{}).Routes())
	defer srv.Close()
	res := post(t, srv, "/api/run", `{"prompt":"/build","meta":{"kind":"build","laneKey":"b1"}}`)
	if res.StatusCode != 201 {
		t.Fatalf("status %d, want 201", res.StatusCode)
	}
	if sp.lastEngine != domain.EngineClaude {
		t.Fatalf("an omitted engine must default to claude, got %q", sp.lastEngine)
	}
}
