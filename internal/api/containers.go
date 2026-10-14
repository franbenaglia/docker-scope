package api

import (
	"cmp"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"

	"github.com/franbenaglia/docker-scope/internal/collector"
	"github.com/franbenaglia/docker-scope/internal/inventory"
)

// List limits.
const (
	defaultLimit = 500
	maxLimit     = 500
)

// ContainerListDTO is the response of GET /api/containers.
type ContainerListDTO struct {
	DataStateDTO
	Items  []ContainerDTO `json:"items"`
	Total  int            `json:"total"` // matches before pagination
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
	// Projects lists every Compose project present (unfiltered), for grouping UIs.
	Projects []string `json:"projects"`
}

// ContainerDetailDTO is the response of GET /api/containers/{id}.
type ContainerDetailDTO struct {
	DataStateDTO
	ContainerDTO
	ImageID             string            `json:"image_id"`
	Labels              map[string]string `json:"labels"`
	RestartMaxRetry     int               `json:"restart_max_retry"`
	LogDriver           string            `json:"log_driver"`
	TTY                 bool              `json:"tty"`
	HealthFailingStreak int               `json:"health_failing_streak"`
	Error               string            `json:"error,omitempty"` // Docker's State.Error
}

type listQuery struct {
	q       string
	states  map[string]bool
	health  map[string]bool
	project *string
	service *string
	sort    string
	desc    bool
	limit   int
	offset  int
}

var (
	validStates = []string{"running", "paused", "restarting", "stopped"}
	validHealth = []string{inventory.HealthNone, inventory.HealthStarting, inventory.HealthHealthy, inventory.HealthUnhealthy}
	validSorts  = []string{"name", "state", "cpu", "mem", "uptime", "restarts", "project"}
)

func parseListQuery(r *http.Request) (listQuery, *apiError) {
	v := r.URL.Query()
	lq := listQuery{q: strings.ToLower(strings.TrimSpace(v.Get("q"))), sort: "name", limit: defaultLimit}
	if len(lq.q) > 200 {
		return lq, badParam("q demasiado largo (máx. 200)")
	}
	var err *apiError
	if lq.states, err = parseSet(v.Get("state"), validStates, "state"); err != nil {
		return lq, err
	}
	if lq.health, err = parseSet(v.Get("health"), validHealth, "health"); err != nil {
		return lq, err
	}
	if v.Has("project") {
		p := v.Get("project")
		lq.project = &p // "" selects containers outside Compose
	}
	if v.Has("service") {
		s := v.Get("service")
		lq.service = &s
	}
	if s := v.Get("sort"); s != "" {
		if !slices.Contains(validSorts, s) {
			return lq, badParam("sort debe ser uno de: " + strings.Join(validSorts, ", "))
		}
		lq.sort = s
	}
	switch v.Get("order") {
	case "", "asc":
	case "desc":
		lq.desc = true
	default:
		return lq, badParam("order debe ser asc o desc")
	}
	if s := v.Get("limit"); s != "" {
		n, e := strconv.Atoi(s)
		if e != nil || n < 1 || n > maxLimit {
			return lq, badParam("limit debe estar entre 1 y " + strconv.Itoa(maxLimit))
		}
		lq.limit = n
	}
	if s := v.Get("offset"); s != "" {
		n, e := strconv.Atoi(s)
		if e != nil || n < 0 {
			return lq, badParam("offset debe ser un entero ≥ 0")
		}
		lq.offset = n
	}
	return lq, nil
}

func parseSet(raw string, valid []string, name string) (map[string]bool, *apiError) {
	if raw == "" {
		return nil, nil
	}
	set := map[string]bool{}
	for p := range strings.SplitSeq(raw, ",") {
		p = strings.TrimSpace(p)
		if !slices.Contains(valid, p) {
			return nil, badParam(name + " debe ser una lista de: " + strings.Join(valid, ", "))
		}
		set[p] = true
	}
	return set, nil
}

// stateGroup maps Docker states to the filter vocabulary.
func stateGroup(s container.ContainerState) string {
	switch s {
	case container.StateRunning, container.StatePaused, container.StateRestarting:
		return string(s)
	default:
		return "stopped"
	}
}

func (lq listQuery) match(c *inventory.Container) bool {
	if lq.states != nil && !lq.states[stateGroup(c.State)] {
		return false
	}
	if lq.health != nil {
		h := c.HealthCurrent()
		if c.Health == inventory.HealthNone {
			h = inventory.HealthNone
		}
		if !lq.health[h] {
			return false
		}
	}
	if lq.project != nil && c.ComposeProject != *lq.project {
		return false
	}
	if lq.service != nil && c.ComposeService != *lq.service {
		return false
	}
	if lq.q != "" {
		hay := strings.ToLower(c.Name + "\x00" + c.Image + "\x00" + c.ComposeProject + "\x00" + c.ComposeService)
		if !strings.Contains(hay, lq.q) && !strings.HasPrefix(c.ID, lq.q) {
			return false
		}
	}
	return true
}

// sortContainers orders in place. Missing values (no metrics) always go last.
func (lq listQuery) sortContainers(list []*inventory.Container, latest map[string]collector.Sample) {
	num := func(c *inventory.Container) *float64 {
		s, ok := latest[c.ID]
		switch lq.sort {
		case "cpu":
			if ok && c.Running() {
				return s.CPUPercent
			}
		case "mem":
			if ok && c.Running() && s.MemUsage != nil {
				v := float64(*s.MemUsage)
				return &v
			}
		case "uptime":
			if c.Running() && !c.StartedAt.IsZero() {
				v := float64(-c.StartedAt.UnixNano()) // earlier start = longer uptime
				return &v
			}
		case "restarts":
			v := float64(c.ObservedRestarts)
			return &v
		}
		return nil
	}
	dir := 1
	if lq.desc {
		dir = -1
	}
	slices.SortStableFunc(list, func(a, b *inventory.Container) int {
		var r int
		switch lq.sort {
		case "name":
			r = strings.Compare(a.Name, b.Name)
		case "state":
			r = strings.Compare(stateGroup(a.State), stateGroup(b.State))
		case "project":
			r = strings.Compare(a.ComposeProject+"\x00"+a.ComposeService, b.ComposeProject+"\x00"+b.ComposeService)
		default:
			na, nb := num(a), num(b)
			switch {
			case na == nil && nb == nil:
			case na == nil:
				return 1
			case nb == nil:
				return -1
			default:
				r = cmp.Compare(*na, *nb)
			}
		}
		if r == 0 {
			return strings.Compare(a.Name, b.Name) // stable tie-break, always ascending
		}
		return r * dir
	})
}

func (s *Server) handleContainers(w http.ResponseWriter, r *http.Request) {
	lq, perr := parseListQuery(r)
	if perr != nil {
		perr.write(w)
		return
	}
	snap := s.Inventory.Snapshot()
	latest := s.Metrics.LatestAll()
	now := s.Now()

	projects := map[string]bool{}
	var matched []*inventory.Container
	for _, c := range snap.Containers {
		if c.ComposeProject != "" {
			projects[c.ComposeProject] = true
		}
		if lq.match(c) {
			matched = append(matched, c)
		}
	}
	lq.sortContainers(matched, latest)

	out := ContainerListDTO{
		DataStateDTO: dataState(snap), Total: len(matched), Limit: lq.limit, Offset: lq.offset,
		Items: []ContainerDTO{}, Projects: slices.Sorted(maps.Keys(projects)),
	}
	if lq.offset < len(matched) {
		for _, c := range matched[lq.offset:min(len(matched), lq.offset+lq.limit)] {
			out.Items = append(out.Items, s.containerWithMetrics(c, latest, now))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid_parameter", "id de contenedor inválido")
		return
	}
	snap := s.Inventory.Snapshot()
	idx := slices.IndexFunc(snap.Containers, func(c *inventory.Container) bool { return c.ID == id })
	if idx < 0 {
		writeError(w, http.StatusNotFound, "not_found", "contenedor inexistente (se identifica por el ID completo)")
		return
	}
	c := snap.Containers[idx]
	out := ContainerDetailDTO{
		DataStateDTO: dataState(snap),
		ContainerDTO: s.containerWithMetrics(c, s.Metrics.LatestAll(), s.Now()),
		ImageID:      c.ImageID, Labels: c.Labels, RestartMaxRetry: c.RestartMaxRetry,
		LogDriver: c.LogDriver, TTY: c.TTY, HealthFailingStreak: c.HealthFailingStreak, Error: c.Error,
	}
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	writeJSON(w, http.StatusOK, out)
}

// apiError is a validation error to be written as the uniform error body.
type apiError struct {
	status  int
	code    string
	message string
}

func badParam(msg string) *apiError {
	return &apiError{status: http.StatusBadRequest, code: "invalid_parameter", message: msg}
}

func (e *apiError) write(w http.ResponseWriter) { writeError(w, e.status, e.code, e.message) }
