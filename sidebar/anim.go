package main

// Which frame a machine's Pokémon shows at time t. Pure: no I/O, no timers.
//
// Every frame is a function of the wall clock, so every workspace and every
// machine (NTP-synced) shows the same loop frame at the same moment:
//   - 125 ms tick for the fast loops (poison, sleep),
//   - 500 ms beat for the idle bob, which is also where one-shots start,
//   - one-shot frames 50 ms apart (the game's switch-out is ~0.4 s), handed
//     back all at once with a time for each so they can be sent ahead.
//
// What plays, highest priority first:
//   poison  loop while blocked (an agent is waiting on the user)
//   recall  once, when the block is answered, followed by
//   sendout once (also when a new agent appears)
//   tackle  once, when HEAD moves (a commit)
//   harden  once, when a remote-tracking ref moves (a push)
//   sleep   loop at night unless an agent is working
//   bob     otherwise
// After a recall the Pokémon stays in its ball until the next send-out.

const (
	tickMS = 125
	bobMS  = 500
	shotMS = 50
)

var oneShots = []string{"sendout", "recall", "harden", "tackle"}

// Anim: one species from frames.json, animation name -> frames.
type Anim map[string][]string

// ShotEntry: a queued one-shot; Done marks who asked ("" = the daemon).
type ShotEntry struct {
	Name, Done string
}

type AnimState struct {
	Queue      []*ShotEntry
	Shot       *ShotEntry
	ShotStart  int64
	ShotEnd    int64
	Parked     *string
	WasBlocked bool
	Fresh      bool
}

func newAnimState() *AnimState { return &AnimState{Fresh: true} }

func enqueue(st *AnimState, name, done string) {
	if done == "" {
		for _, e := range st.Queue {
			if e.Name == name {
				return
			}
		}
	}
	st.Queue = append(st.Queue, &ShotEntry{Name: name, Done: done})
}

func seq(a Anim, name string) []string {
	if f := a[name]; len(f) > 0 {
		return f
	}
	return nil
}

type Timed struct {
	At    int64
	Frame string
}

// Pick: one of
//
//	Frame (+Busy)              show this frame at t
//	Shot, Entry, End (+Busy)   a one-shot starts; Entry.Done is the
//	                           requester's marker, End its end
//	Hold (+Busy)               a one-shot is still playing
type Pick struct {
	Frame string
	Busy  bool
	Shot  []Timed
	Entry *ShotEntry
	End   int64
	Hold  bool
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func ceilDiv(a, b int64) int64 { return -floorDiv(-a, b) }

// pick: the frame for a workspace in state st with rolled-up status at tick
// boundary t (ms).
func pick(st *AnimState, status string, a Anim, t int64, night bool) Pick {
	tickNo := floorDiv(t, tickMS)
	beat := floorDiv(t, bobMS)
	idle := seq(a, "bob")
	if idle == nil {
		idle = seq(a, "base")
	}
	if idle == nil {
		return Pick{Hold: true, Busy: false}
	}
	loop := func(name string) string {
		f := seq(a, name)
		if f == nil {
			f = idle
		}
		return f[tickNo%int64(len(f))]
	}
	blocked := status == "blocked"

	if blocked && st.Shot == nil {
		return Pick{Frame: loop("poison"), Busy: true}
	}
	if st.Shot == nil {
		for len(st.Queue) > 0 {
			next := st.Queue[0]
			st.Queue = st.Queue[1:]
			frames := seq(a, next.Name)
			if frames == nil {
				continue // the manifest lacks it: skip
			}
			st.Shot = next
			st.ShotStart = ceilDiv(t, bobMS) * bobMS
			st.ShotEnd = st.ShotStart + int64(len(frames))*shotMS
			if next.Name == "recall" {
				p := frames[len(frames)-1]
				st.Parked = &p
			}
			if next.Name == "sendout" {
				st.Parked = nil
			}
			shot := make([]Timed, len(frames))
			for i, f := range frames {
				shot[i] = Timed{At: st.ShotStart + int64(i)*shotMS, Frame: f}
			}
			return Pick{Shot: shot, Busy: true, Entry: next, End: st.ShotEnd}
		}
	}
	if st.Shot != nil {
		if t < st.ShotEnd {
			return Pick{Hold: true, Busy: true}
		}
		st.Shot = nil
		return pick(st, status, a, t, night)
	}
	if st.Parked != nil {
		return Pick{Frame: *st.Parked}
	}
	if night && status != "working" && seq(a, "sleep") != nil {
		return Pick{Frame: loop("sleep"), Busy: true}
	}
	return Pick{Frame: idle[beat%int64(len(idle))]}
}

// observe feeds one observation of a workspace's state and queues the
// implied one-shots. scanned: false on the daemon's first look (existing
// agents aren't new).
func observe(st *AnimState, status string, hasAgent, scanned bool) {
	blocked := status == "blocked"
	if st.WasBlocked && !blocked {
		enqueue(st, "recall", "")
		enqueue(st, "sendout", "")
	}
	if hasAgent && st.Fresh && scanned {
		enqueue(st, "sendout", "")
	}
	if hasAgent {
		st.Fresh = false
	}
	st.WasBlocked = blocked
}

// rollup: the status of several agents that most needs showing.
func rollup(statuses []string) string {
	for _, s := range []string{"blocked", "working", "done", "idle"} {
		if contains(statuses, s) {
			return s
		}
	}
	if len(statuses) > 0 {
		return "unknown"
	}
	return "none"
}

// nextBoundary: the next boundary on the global grid of period, at least
// prep ms ahead.
func nextBoundary(now, period, prep, after int64) int64 {
	next := after + 1
	if now+prep > next {
		next = now + prep
	}
	return ceilDiv(next, period) * period
}
