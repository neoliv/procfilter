package procfilter

//	"fmt"

// TODO add IO stats?
// TODO add percent for memory?

// TODO convert the maps used during apply() to slices?
var allStats = map[tPid]stat{}
var newStats = map[tPid]stat{}

type tPid int32   // A PID, note that once a set of PIDs is packed as a single packStat it gets a negative pseudo ID
type tStamp uint8 // A stamp used to identify samples.

/* A real process or a group of (packed) processes will implement this interface to get to the underlying statistics. */
type stat interface {
	PID() tPid
	UID() (int32, error)
	User() (string, error)
	GID() (int32, error)
	Group() (string, error)
	RSS() (uint64, error)
	VSZ() (uint64, error)
	Swap() (uint64, error)
	CPU() (float32, error)
	IO() (uint64, error)
	IObps() (uint64, error)
	ProcessNumber() uint64
	ThreadNumber() (uint64, error)
	FDNumber() (uint64, error)
	Path() (string, error)
	Exe() (string, error)
	Cmd() (string, error)
	CmdLine() (string, error)
	ChildrenPIDs(int) []tPid
	Var(string) string
	PVars() *(map[string]string) // Pointer to the inner map.
}

type stats struct {
	stamp    tStamp // Stamp the pid2Stat to know to what sample it refers.
	pid2Stat map[tPid]stat
}

// A stamp to know when we change from one sample to the next
var stamp tStamp

// Fast lookup for UID -> user name
var uid2User = map[int32]string{}

// Sorting helpers
type sortCrit int
type statSlice []stat
type byRSS statSlice
type byVSZ statSlice
type bySwap statSlice
type byCPU statSlice
type byProcessNumber statSlice
type byThreadNumber statSlice
type byFDNumber statSlice
type byIO statSlice
type byIObps statSlice

func (s byRSS) Len() int {
	return len(s)
}

func (s byRSS) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s byRSS) Less(i, j int) bool {
	// use > (instead of <) to reverse the sort order and get the biggest first
	iv, _ := s[i].RSS()
	jv, _ := s[j].RSS()
	return iv > jv
}

func (s byVSZ) Len() int {
	return len(s)
}

func (s byVSZ) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s byVSZ) Less(i, j int) bool {
	// use > (instead of <) to reverse the sort order and get the biggest first
	iv, _ := s[i].VSZ()
	jv, _ := s[j].VSZ()
	return iv > jv
}

func (s bySwap) Len() int {
	return len(s)
}

func (s bySwap) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s bySwap) Less(i, j int) bool {
	// use > (instead of <) to reverse the sort order and get the biggest first
	iv, _ := s[i].Swap()
	jv, _ := s[j].Swap()
	return iv > jv
}

func (s byProcessNumber) Len() int {
	return len(s)
}

func (s byProcessNumber) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s byProcessNumber) Less(i, j int) bool {
	// use > (instead of <) to reverse the sort order and get the biggest first
	iv := s[i].ProcessNumber()
	jv := s[j].ProcessNumber()
	return iv > jv
}

func (s byThreadNumber) Len() int {
	return len(s)
}

func (s byThreadNumber) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s byThreadNumber) Less(i, j int) bool {
	// use > (instead of <) to reverse the sort order and get the biggest first
	iv, _ := s[i].ThreadNumber()
	jv, _ := s[j].ThreadNumber()
	return iv > jv
}

func (s byFDNumber) Len() int {
	return len(s)
}

func (s byFDNumber) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s byFDNumber) Less(i, j int) bool {
	// use > (instead of <) to reverse the sort order and get the biggest first
	iv, _ := s[i].FDNumber()
	jv, _ := s[j].FDNumber()
	return iv > jv
}

func (s byCPU) Len() int {
	return len(s)
}

func (s byCPU) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s byCPU) Less(i, j int) bool {
	// use > (instead of <) to reverse the sort order and get the biggest first
	iv, _ := s[i].CPU()
	jv, _ := s[j].CPU()
	return iv > jv
}

func (s byIO) Len() int {
	return len(s)
}

func (s byIO) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s byIO) Less(i, j int) bool {
	// use > (instead of <) to reverse the sort order and get the biggest first
	iv, _ := s[i].IO()
	jv, _ := s[j].IO()
	return iv > jv
}

func (s byIObps) Len() int {
	return len(s)
}

func (s byIObps) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s byIObps) Less(i, j int) bool {
	// use > (instead of <) to reverse the sort order and get the biggest first
	iv, _ := s[i].IObps()
	jv, _ := s[j].IObps()
	return iv > jv
}

func processOldForks() {
	apsMutex.Lock()
	forksMutex.Lock()
	var rf, vanished int
	for pid := range forks {
		_, known := allProcStats[pid]
		if known {
			// The most common case. We got this one elsewhere (probably an exec() done after the fork().
			continue
		}
		rf++
		ts := forks[pid]
		if !addNewProcStat(pid, ts, false) {
			vanished++
		}
	}
	// Clean the whole map (all fork events have been processed above)
	l := len(forks)
	if l > 0 {
		trace("processed %d deferred fork events. %d / %d (vanished / real forks).", len(forks), vanished, rf)
		// Otherwise the map is still empty and ca be reused for the nex interval.
		forks = make(map[tPid]uint64, l) // plan to store about the same number of fork events as during th previous interval.
	}
	forksMutex.Unlock()
	apsMutex.Unlock()
}

// resetGlobalStatSets update the (global) stats structures for the current sample. (ie: purge dead PIDs, and get new ones)
func resetGlobalStatSets() {
	rebuildAllStats()
	resetAllPackStats()
}

// reset checks if the stats are relevant to the current sample, if not it resets them. Returns true if we changed sample (reset occured)
func (s *stats) reset() bool {
	if s.pid2Stat == nil || s.stamp != stamp {
		s.pid2Stat = map[tPid]stat{}
		s.stamp = stamp
		return true
	}
	return false
}

// unpackAsSlice collect all procStats from all statss (unpack packStats).
func (s stats) unpackAsSlice(ss []*procStat) []*procStat {
	return unpackMapAsSlice(s.pid2Stat, ss)
}

// unpackAsMap collect all procStats from all statss (unpack packStats).
func (s stats) unpackAsMap(sm map[tPid]*procStat) map[tPid]*procStat {
	return unpackMapAsMap(s.pid2Stat, sm)
}
