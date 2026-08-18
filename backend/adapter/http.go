// Package adapter translates HTTP requests into usecase calls and back. It imports
// usecase + domain; nothing imports adapter except cmd wiring.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/drayanaindra/saki-cli/backend/domain"
	"github.com/drayanaindra/saki-cli/backend/usecase"
)

const maxBody = 1 << 20 // 1 MiB request-body cap

const (
	hdrContentType = "Content-Type"
	errCwdRequired = "cwd is required"
)

// Proxy reverse-proxies to apps/server for run-kinds/routes Go does not own. *infra.HTTPProxy
// implements it (a superset of usecase.Proxy — it adds the long-lived SSE passthrough).
type Proxy interface {
	Forward(method, path, cookie string, body []byte) (int, []byte, error)
	GetRuns(cookie string) ([]map[string]any, error)
	StreamEvents(ctx context.Context, id, cookie string, w http.ResponseWriter)
}

// Handler serves the Go-owned run-vertical + git-write routes and reverse-proxies the un-owned ones.
type Handler struct {
	branch    usecase.BranchService
	runs      usecase.RunService
	engine    *usecase.BuildEngineService // F6: build spawn + auto-resume engine (Go-owned builds)
	list      usecase.ListService
	stream    usecase.StreamService
	stop      usecase.StopService
	proxy     Proxy
	gitWrite  usecase.GitWriteService
	roadmap   usecase.RoadmapService
	workitems usecase.WorkItemsService
	prd       usecase.PrdService
	lock      usecase.LockService
	blockers  usecase.BlockersService
	sliceMeta usecase.SliceMetaService
	resolve   usecase.ResolveBlockerService
	planTrack usecase.PlanTrackService
	doctor    usecase.DoctorService // F2 slice 1: engine provisioning verdict
	initEnv   usecase.InitEnvService
}

// NewHandler wires the usecase services into the HTTP handler.
// initEnv is a REQUIRED parameter, not a variadic. `Routes` registers POST /api/init-env
// unconditionally, so a handler built without the service would carry a zero-value InitEnvService
// whose provisioner and proofs ports are nil — a registered route backed by a nil port, which
// nil-panics on the first real request. That is the exact hazard TestDoctorHandler_RealWiring was
// written to catch for DoctorService; making the parameter required turns it into a compile error.
func NewHandler(branch usecase.BranchService, runs usecase.RunService, engine *usecase.BuildEngineService, list usecase.ListService, stream usecase.StreamService, stop usecase.StopService, proxy Proxy, gitWrite usecase.GitWriteService, roadmap usecase.RoadmapService, workitems usecase.WorkItemsService, prd usecase.PrdService, lock usecase.LockService, blockers usecase.BlockersService, sliceMeta usecase.SliceMetaService, resolve usecase.ResolveBlockerService, planTrack usecase.PlanTrackService, doctor usecase.DoctorService, initEnv usecase.InitEnvService) Handler {
	return Handler{branch: branch, runs: runs, engine: engine, list: list, stream: stream, stop: stop, proxy: proxy, gitWrite: gitWrite, roadmap: roadmap, workitems: workitems, prd: prd, lock: lock, blockers: blockers, sliceMeta: sliceMeta, resolve: resolve, planTrack: planTrack, doctor: doctor, initEnv: initEnv}
}

// Routes returns the mux of Go-owned routes. Patterns are method-anchored (Go 1.22+).
func (h Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	// Liveness. Mirrors apps/server GET /api/health (index.ts:320) but names ITSELF, so a caller can
	// tell which of the two servers answered. Needed because /api/health is the one path both servers
	// own: the proxy/CLI split table (frontend/src/proxy-routes.ts, apps/cli/src/routes.ts) can only
	// route it to Express, so `saki status` probed Express alone and reported a green studio while
	// this backend — which serves runs, roadmap, prd, branch, proto — was dead. OriginGuard-wrapped
	// for parity with the TS global originGuard (index.ts:53), which covers its /api/health too.
	mux.Handle("GET /api/health", OriginGuard(http.HandlerFunc(h.healthHandler)))
	mux.HandleFunc("GET /api/branch", h.branchHandler)
	mux.HandleFunc("POST /api/run", h.postRun)           // local spawn: init (privileged) / build (engine) / other
	mux.HandleFunc("GET /api/runs", h.getRuns)           // union of Go + apps/server runs
	mux.HandleFunc("POST /api/runs", h.forwardRuns)      // hosted route (apps/server) — forward verbatim
	mux.HandleFunc("POST /api/run/{id}/stop", h.stopRun) // stop Go-owned; passthrough un-owned
	mux.HandleFunc("GET /events/{id}", h.events)         // SSE: tail Go-owned; passthrough un-owned
	// F3 · P2 slice 1: git-write bucket (branch list + switch), behind the loopback OriginGuard
	// (BR7) — mounted per-route so the run-vertical routes above are unaffected.
	mux.Handle("GET /api/branches", OriginGuard(http.HandlerFunc(h.branchesHandler)))
	mux.Handle("POST /api/switch-branch", OriginGuard(http.HandlerFunc(h.switchBranchHandler)))
	mux.Handle("POST /api/create-mr", OriginGuard(http.HandlerFunc(h.createMRHandler)))        // slice 2
	mux.Handle("POST /api/merge-to-main", OriginGuard(http.HandlerFunc(h.mergeToMainHandler))) // slice 3
	// F4 · P3 content-bucket reads (slices 1, 2, 4). OriginGuard-wrapped: the TS backend applies a
	// GLOBAL app.use(originGuard) (index.ts:53) that covers EVERY route — GET reads included — so
	// faithful parity (R7) requires the guard here too. (This corrects the earlier slice-1/2 "the TS
	// backend never guards these reads" misread, flagged by the slice-3 review + security audit.)
	// Loopback studio traffic is unaffected; a cross-origin / DNS-rebind read is 403'd like TS.
	mux.Handle("GET /api/roadmap", OriginGuard(http.HandlerFunc(h.roadmapHandler)))          // slice 1
	mux.Handle("GET /api/workitems", OriginGuard(http.HandlerFunc(h.workitemsHandler)))      // slice 1
	mux.Handle("GET /api/prd", OriginGuard(http.HandlerFunc(h.prdHandler)))                  // slice 2
	mux.Handle("GET /api/review", OriginGuard(http.HandlerFunc(h.reviewHandler)))            // slice 2
	mux.Handle("GET /api/review-state", OriginGuard(http.HandlerFunc(h.reviewStateHandler))) // slice 2
	mux.Handle("GET /api/slice-meta", OriginGuard(http.HandlerFunc(h.sliceMetaHandler)))     // slice 4
	mux.Handle("GET /api/blockers", OriginGuard(http.HandlerFunc(h.blockersHandler)))        // slice 4
	// F2 · P2 slice 1: engine provisioning verdict. OriginGuard-wrapped alongside its content-read
	// siblings above — it discloses local profile filesystem paths in its reason field, same class of
	// disclosure as every route in this block.
	mux.Handle("GET /api/doctor", OriginGuard(http.HandlerFunc(h.doctorHandler)))
	mux.Handle("POST /api/init-env", OriginGuard(http.HandlerFunc(h.initEnvHandler)))
	// F4 · P3 slice 3: PRD lock WRITE. OriginGuard-wrapped (BR7) — same TS global-guard parity; R3
	// (domain.ValidateLockRequest) contains the path, OriginGuard the origin (DNS-rebind/CSRF).
	mux.Handle("POST /api/lock-prd", OriginGuard(http.HandlerFunc(h.lockHandler)))
	// F4 · P3 slice 5: resolve-blocker WRITE — durably record blocked-slice answers as ✅ RESOLVED
	// bullets the resumed /build reads. OriginGuard-wrapped (BR7 — TS global-guard parity).
	mux.Handle("POST /api/resolve-blocker", OriginGuard(http.HandlerFunc(h.resolveBlockerHandler)))
	// F4 · P3 slice 6: plan-track roadmap WRITES — the three Plan-track lifecycle transitions on
	// roadmap.md. OriginGuard-wrapped (BR7 — TS global-guard parity). With these three registered, all
	// 12 content routes are on Go (5.1 = 12/12).
	mux.Handle("POST /api/roadmap/plan-start", OriginGuard(http.HandlerFunc(h.planStartHandler)))
	mux.Handle("POST /api/roadmap/plan-ship", OriginGuard(http.HandlerFunc(h.planShipHandler)))
	mux.Handle("POST /api/roadmap/plan-attach", OriginGuard(http.HandlerFunc(h.planAttachHandler)))
	return mux
}

// planStartHandler mirrors apps/server POST /api/roadmap/plan-start: flip a Planned Plan-track item →
// In-progress. OriginGuard-wrapped in Routes() (BR7). 422 on a bad/escaping cwd or id; else a graceful
// {ok:true,changed} / {ok:false,error} at 200 (R6). today's date is injected here (UTC YYYY-MM-DD,
// parity with the TS new Date().toISOString().slice(0,10)) so the usecase stays pure.
func (h Handler) planStartHandler(w http.ResponseWriter, r *http.Request) {
	cwd, id, _ := decodePlanReq(r)
	status, body := h.planTrack.PlanStart(cwd, id, time.Now().UTC().Format(time.DateOnly))
	writeJSON(w, status, body)
}

// planShipHandler mirrors apps/server POST /api/roadmap/plan-ship: advance an In-progress (or Planned)
// Plan-track item → Shipped. Same wiring + guarantees as planStartHandler.
func (h Handler) planShipHandler(w http.ResponseWriter, r *http.Request) {
	cwd, id, _ := decodePlanReq(r)
	status, body := h.planTrack.PlanShip(cwd, id, time.Now().UTC().Format(time.DateOnly))
	writeJSON(w, status, body)
}

// planAttachHandler mirrors apps/server POST /api/roadmap/plan-attach: record a Plan-track item's
// `**Child plan:**` file. Same wiring + guarantees; additionally 422 on a bad planFile (empty /
// multiline / >256). No date is written on attach.
func (h Handler) planAttachHandler(w http.ResponseWriter, r *http.Request) {
	cwd, id, planFile := decodePlanReq(r)
	status, body := h.planTrack.PlanAttach(cwd, id, planFile)
	writeJSON(w, status, body)
}

// decodePlanReq reads the {cwd,id,planFile} body (LimitReader-capped) for the three plan-track routes.
// A non-string field leaves "" → the usecase's 422/no-op gates handle it (parity with the TS body ?? {}).
func decodePlanReq(r *http.Request) (cwd, id, planFile string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	var req struct {
		Cwd      string `json:"cwd"`
		ID       string `json:"id"`
		PlanFile string `json:"planFile"`
	}
	_ = json.Unmarshal(body, &req)
	return req.Cwd, req.ID, req.PlanFile
}

// prdHandler mirrors apps/server GET /api/prd: read an explicit `path` (.md-gated) or the latest PRD
// in `cwd`. The service returns the status + JSON body verbatim (422 on a bad path / neither param,
// {found:false} on a miss, never 500).
func (h Handler) prdHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status, body := h.prd.ReadPrd(q.Get("cwd"), q.Get("path"))
	writeJSON(w, status, body)
}

// reviewHandler mirrors apps/server GET /api/review: read a PRD's -review.md companion. 422 on a
// missing/non-.md path; else {found:true,path,content} or {found:false}.
func (h Handler) reviewHandler(w http.ResponseWriter, r *http.Request) {
	status, body := h.prd.ReadReview(r.URL.Query().Get("path"))
	writeJSON(w, status, body)
}

// reviewStateHandler mirrors apps/server GET /api/review-state: read the derived .prd-review state
// file. 422 on a missing/non-.md path; else {found:true,path,content} or {found:false}.
func (h Handler) reviewStateHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status, body := h.prd.ReadReviewState(q.Get("path"), q.Get("cwd"))
	writeJSON(w, status, body)
}

// lockHandler mirrors apps/server POST /api/lock-prd: validate + freeze a PRD (idempotent R2,
// cwd-contained R3). OriginGuard-wrapped in Routes() (BR7). 422 on a bad/escaping/non-.md path; else a
// graceful {ok:true,locked|alreadyLocked} / {ok:false,error} at 200 (R6). today's date is injected here
// (UTC YYYY-MM-DD, parity with the TS new Date().toISOString().slice(0,10)) so the usecase stays pure.
func (h Handler) lockHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	var req struct {
		Path string `json:"path"`
		Cwd  string `json:"cwd"`
	}
	_ = json.Unmarshal(body, &req) // a non-string path/cwd leaves "" → 422 in the usecase
	today := time.Now().UTC().Format(time.DateOnly)
	status, resp := h.lock.Lock(req.Cwd, req.Path, today)
	writeJSON(w, status, resp)
}

// resolveBlockerHandler mirrors apps/server POST /api/resolve-blocker: record the operator's chosen
// open-question decisions as ✅ RESOLVED bullets in the PRD. OriginGuard-wrapped in Routes() (BR7).
// 422 on a non-.md prdPath / empty|non-array decisions; 404 on a missing PRD; 500 on a write failure
// (faithful to index.ts:643 — NOT the graceful {ok:false} lock-prd uses); else {resolved:<count>}.
// decisions is decoded leniently as []any so an invalid item is skipped per-item (parity with the TS
// per-item continue) instead of rejecting the whole payload.
func (h Handler) resolveBlockerHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	var req struct {
		PrdPath   string `json:"prdPath"`
		Decisions []any  `json:"decisions"`
	}
	_ = json.Unmarshal(body, &req) // a non-string prdPath / non-array decisions leaves the zero value → 422
	status, resp := h.resolve.ResolveBlocker(req.PrdPath, req.Decisions)
	writeJSON(w, status, resp)
}

// sliceMetaHandler mirrors apps/server GET /api/slice-meta: the per-slice feat commit via git log
// --grep. 422 on a missing cwd/n; else {commit} (null when no commit / git error, never 500).
func (h Handler) sliceMetaHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status, body := h.sliceMeta.SliceMeta(q.Get("cwd"), q.Get("n"))
	writeJSON(w, status, body)
}

// blockersHandler mirrors apps/server GET /api/blockers: why slice n is blocked + the decisions it
// waits on. 422 on a bad prd/non-integer n; 404 on a missing PRD; else {n,reason,deferred,questions}.
func (h Handler) blockersHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status, body := h.blockers.ReadBlockers(q.Get("cwd"), q.Get("prd"), q.Get("n"))
	writeJSON(w, status, body)
}

// roadmapHandler mirrors apps/server GET /api/roadmap: cwd-contained (422 on a bad/escaping cwd),
// degrades on malformed input, never 500s. The service returns the status + JSON body verbatim.
func (h Handler) roadmapHandler(w http.ResponseWriter, r *http.Request) {
	status, body := h.roadmap.ReadRoadmap(r.URL.Query().Get("cwd"))
	writeJSON(w, status, body)
}

// doctorHandler serves F2's pre-dispatch provisioning verdict. `?profile=<dir>` pins the probed
// profile the same way the proofs' own configDir does; absent = the default profile.
func (h Handler) doctorHandler(w http.ResponseWriter, r *http.Request) {
	var configDir *string
	if p := r.URL.Query().Get("profile"); p != "" {
		configDir = &p
	}
	writeJSON(w, http.StatusOK, map[string]any{"engines": h.doctor.Check(configDir)})
}

func (h Handler) initEnvHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cwd     string `json:"cwd"`
		Engine  string `json:"engine"`
		Profile string `json:"profile"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&req); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid request body"})
		return
	}
	var profile *string
	if req.Profile != "" {
		profile = &req.Profile
	}
	status, body := h.initEnv.Provision(usecase.ProvisionRequest{Cwd: req.Cwd, Engine: domain.RunEngine(req.Engine), Profile: profile})
	writeJSON(w, status, body)
}

// workitemsHandler mirrors apps/server GET /api/workitems: 422 when cwd is missing, else the PRD/plan
// work queue with done items excluded (parity: index.ts filters status!='done' at the route).
func (h Handler) workitemsHandler(w http.ResponseWriter, r *http.Request) {
	cwd := r.URL.Query().Get("cwd")
	if cwd == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": errCwdRequired})
		return
	}
	prds, plans := h.workitems.FindWorkItems(cwd)
	writeJSON(w, http.StatusOK, map[string]any{
		"prds":  notDone(prds),
		"plans": notDone(plans),
	})
}

// notDone drops done items (the /api/workitems route filter), preserving order + JSON shape.
func notDone(items []domain.WorkItem) []domain.WorkItem {
	out := []domain.WorkItem{}
	for _, i := range items {
		if i.Status != domain.WorkDone {
			out = append(out, i)
		}
	}
	return out
}

// branchesHandler mirrors apps/server GET /api/branches: 422 when cwd is missing, a non-repo cwd
// → {"branches":[]} (index.ts:410-411), else {"branches":[...]}.
func (h Handler) branchesHandler(w http.ResponseWriter, r *http.Request) {
	branches, err := h.gitWrite.Branches(domain.Repo{Dir: r.URL.Query().Get("cwd")})
	if errors.Is(err, usecase.ErrCwdRequired) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": errCwdRequired})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "branch list failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"branches": branches})
}

// switchBranchHandler mirrors apps/server POST /api/switch-branch: 422 on a missing field or (on
// create) an invalid branch name (BR5); else a graceful {ok,branch} / {ok:false,error} at 200.
func (h Handler) switchBranchHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	var req struct {
		Cwd    string `json:"cwd"`
		Branch string `json:"branch"`
		Create bool   `json:"create"`
	}
	_ = json.Unmarshal(body, &req) // a non-string cwd/branch leaves "" → 422 below
	res, err := h.gitWrite.Switch(domain.Repo{Dir: req.Cwd}, req.Branch, req.Create)
	var ve usecase.ValidationError
	if errors.As(err, &ve) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": ve.Msg})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "switch failed"})
		return
	}
	if !res.OK {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": res.Error})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "branch": res.Branch})
}

// createMRHandler mirrors apps/server POST /api/create-mr: 422 on a missing cwd; else a graceful
// {ok:true,url} / {ok:false,error} at 200 (BR2) — no-remote / detached-HEAD / source==target /
// a glab failure all return ok:false, never a throw.
func (h Handler) createMRHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	var req struct {
		Cwd string `json:"cwd"`
	}
	_ = json.Unmarshal(body, &req)
	res, err := h.gitWrite.CreateMR(domain.Repo{Dir: req.Cwd})
	var ve usecase.ValidationError
	if errors.As(err, &ve) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": ve.Msg})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "create MR failed"})
		return
	}
	if !res.OK {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": res.Error})
		return
	}
	out := map[string]any{"ok": true}
	if res.URL != "" {
		out["url"] = res.URL // parity: url omitted when glab printed none (index.ts `?? undefined`)
	}
	writeJSON(w, http.StatusOK, out)
}

// mergeToMainHandler mirrors apps/server POST /api/merge-to-main: 422 on a missing cwd; else a
// graceful {ok:true,branch,source} / {ok:false,error} at 200 (BR2) — detached-HEAD / no-local-base
// / source==target / a dirty-tree switch / a merge conflict all return ok:false, never a throw.
func (h Handler) mergeToMainHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	var req struct {
		Cwd string `json:"cwd"`
	}
	_ = json.Unmarshal(body, &req)
	res, err := h.gitWrite.MergeToMain(domain.Repo{Dir: req.Cwd})
	var ve usecase.ValidationError
	if errors.As(err, &ve) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": ve.Msg})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "merge failed"})
		return
	}
	if !res.OK {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": res.Error})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "branch": res.Branch, "source": res.Source})
}

// stopRun stops a Go-owned run (SIGTERM its group) → 200 {stopped:true}; an unknown/finished run →
// 404; an un-owned (build) id is forwarded to apps/server.
func (h Handler) stopRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, ok := h.runs.Owned(id)
	if !ok {
		h.forward(w, r, http.MethodPost, "/api/run/"+url.PathEscape(id)+"/stop", nil)
		return
	}
	// A build routes to the engine's Stop (cancels a recovering chain's pendingResume too); every
	// other Go-owned kind uses the plain stop service.
	stopped := false
	if run.Meta != nil && run.Meta.Kind.IsBuild() {
		stopped = h.engine.Stop(id)
	} else {
		stopped = h.stop.Stop(id)
	}
	if !stopped {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no running run with that id"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stopped": true})
}

// events streams a run's SSE. A Go-owned run is tailed from its <id>.out; an un-owned id (a build
// run) is passthrough-proxied to apps/server, so StreamPanel works for builds during coexistence.
func (h Handler) events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := h.runs.Owned(id); !ok {
		h.proxy.StreamEvents(r.Context(), id, r.Header.Get("Cookie"), w)
		return
	}
	w.Header().Set(hdrContentType, "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	emit := func(ev usecase.StreamEvent) {
		b, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", b)
		_ = rc.Flush()
	}
	end := func(status string, exit *int) {
		b, _ := json.Marshal(map[string]any{"status": status, "exitCode": exit})
		fmt.Fprintf(w, "event: end\ndata: %s\n\n", b)
		_ = rc.Flush()
	}
	h.stream.Stream(r.Context(), id, emit, end)
}

// serviceName identifies THIS server in a health response. Deliberately distinct from apps/server's
// "pipeline-studio-server" (index.ts:321): a probe that gets the same name from both origins cannot
// prove it reached two different processes.
const serviceName = "saki-backend"

// healthHandler answers the liveness probe. Depends on nothing — it must answer while every usecase
// below is still deciding whether it can, so "is this process listening" is never confused with
// "is this repo in a good state".
func (h Handler) healthHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": serviceName})
}

// branchHandler mirrors apps/server GET /api/branch: 422 when cwd is missing, else
// {"branch": <name|null>}.
func (h Handler) branchHandler(w http.ResponseWriter, r *http.Request) {
	repo := domain.Repo{Dir: r.URL.Query().Get("cwd")}
	name, err := h.branch.Current(repo)
	if errors.Is(err, usecase.ErrCwdRequired) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": errCwdRequired})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "branch read failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"branch": name})
}

// postRun spawns a run locally: init → a privileged --dangerously-skip-permissions spawn (loopback +
// origin guarded); build → the Go auto-resume engine (F6); every other kind → the plain run service.
// As of F7 · P6 slice 4 NOTHING is reverse-proxied from here — init used to forward to apps/server for
// the elevated flag, but that forward is retired now that init is Go-owned behind the 127.0.0.1 bind.
func (h Handler) postRun(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	var req struct {
		Prompt         string           `json:"prompt"`
		Cwd            *string          `json:"cwd"`
		ConfigDir      *string          `json:"configDir"`
		Meta           *domain.Meta     `json:"meta"`
		IdempotencyKey *string          `json:"idempotencyKey"`
		Engine         domain.RunEngine `json:"engine"`
	}
	_ = json.Unmarshal(body, &req) // a non-string prompt leaves Prompt="" → 422 below
	// E26 — the engine is validated at the boundary: only the two known runtimes (or empty = claude).
	engine, err := parseRunEngine(req.Engine)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	// init spawns claude with --dangerously-skip-permissions (the only kind that gets it). Two
	// independent controls keep the elevated flag off-limits: the 127.0.0.1 bind refuses off-host TCP
	// (§10 rule 5), and this inline localhostOnly guard refuses a cross-origin / DNS-rebind POST from the
	// operator's OWN browser (localhost-CSRF) — the bind alone can't stop a malicious page on loopback.
	// Guard is scoped to init (build/other kinds unchanged — Non-Goal). BR5.
	if req.Meta != nil && req.Meta.Kind.IsInit() {
		if !localhostOnly(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-origin blocked"})
			return
		}
		run, deduped, err := h.runs.SpawnInit(usecase.SpawnReq{Prompt: req.Prompt, Cwd: req.Cwd, ConfigDir: req.ConfigDir, Meta: req.Meta, Engine: engine})
		if err != nil {
			writeSpawnResult(w, "", err)
			return
		}
		if deduped { // re-adopt the in-flight init; no second privileged process (parity index.ts:768)
			writeJSON(w, http.StatusOK, map[string]any{"runId": run.ID, "deduped": true})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"runId": run.ID})
		return
	}
	// build → the Go auto-resume engine (F6); every other kind → the plain run service.
	if req.Meta != nil && req.Meta.Kind.IsBuild() {
		run, err := h.engine.SpawnBuild(usecase.BuildSpawnReq{Prompt: req.Prompt, Cwd: req.Cwd, ConfigDir: req.ConfigDir, Meta: req.Meta, IdempotencyKey: req.IdempotencyKey, Engine: engine})
		writeSpawnResult(w, run.ID, err)
		return
	}
	run, err := h.runs.Spawn(usecase.SpawnReq{Prompt: req.Prompt, Cwd: req.Cwd, ConfigDir: req.ConfigDir, Meta: req.Meta, Engine: engine})
	writeSpawnResult(w, run.ID, err)
}

// parseRunEngine validates an engine request value at the boundary (E26): only the known runtimes
// (or empty = the claude default) are accepted; anything else is a 422.
func parseRunEngine(e domain.RunEngine) (domain.RunEngine, error) {
	switch domain.ResolveEngine(e) {
	case domain.EngineClaude, domain.EngineOpencode, domain.EngineCodex:
		return domain.ResolveEngine(e), nil
	default:
		return "", fmt.Errorf("engine must be one of %q, %q, %q", domain.EngineClaude, domain.EngineOpencode, domain.EngineCodex)
	}
}

// writeSpawnResult maps a spawn error to the shared status codes (422 no-prompt, 500 unwritable/other,
// 201 {runId}) — used by both the build-engine and the plain run-service spawn paths.
func writeSpawnResult(w http.ResponseWriter, runID string, err error) {
	switch {
	case errors.Is(err, usecase.ErrNoPrompt):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "prompt (string) is required"})
	case errors.Is(err, usecase.ErrRunsDirUnwritable):
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "runs dir is not writable"})
	case errors.Is(err, usecase.ErrBinaryNotFound), errors.Is(err, usecase.ErrEngineNotProvisioned):
		// Both carry an actionable diagnosis (which binary is missing; which profile lacks which
		// plugin/skill, and the installer to run) — surfacing err.Error() is what makes the refusal
		// something the operator can act on rather than a bare failure.
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "spawn failed"})
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"runId": runID})
	}
}

// getRuns returns the union of Go's non-build runs and apps/server's runs.
func (h Handler) getRuns(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.list.List(r.URL.Query().Get("cwd"), r.Header.Get("Cookie")))
}

// forwardRuns relays the hosted POST /api/runs (a different route sharing the /api/runs path) to
// apps/server — Vite can't method-split a path, so Go owns /api/runs and forwards the POST.
func (h Handler) forwardRuns(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	h.forward(w, r, http.MethodPost, "/api/runs", body)
}

// forward relays a request to apps/server and writes its status + body back verbatim.
func (h Handler) forward(w http.ResponseWriter, r *http.Request, method, path string, body []byte) {
	status, respBody, err := h.proxy.Forward(method, path, r.Header.Get("Cookie"), body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "upstream unreachable"})
		return
	}
	w.Header().Set(hdrContentType, "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(respBody)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set(hdrContentType, "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
