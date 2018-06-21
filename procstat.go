package procfilter

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	//	"github.com/shirou/gopsutil/process"
)

type Status uint8 // Procfilter State of a process.

const (
	NEW Status = iota
	YOUNG
	ADULT
	DEAD
)

// Used when a UID/GID does not match a known user/group
var strIdUnknown = "[unknown]"

// Keep all real processes stats.
var apsMutex = sync.Mutex{}
var allProcStats = map[tPid]*procStat{}

// Keep all fork event (a trick to avoid a double access to /proc/[pid]/ for every fork/exec)
var forksMutex = sync.Mutex{}
var forks = map[tPid]uint64{}

/* Stats for a process */
type procStat struct {
	pid         tPid   // this process PID
	ppid        tPid   // parent PID
	tgid        tPid   // thread group ID (!= PID if in a thread)
	startTime   uint64 // start time as Unix nanos.
	deathTime   uint64 // exit/death time as Unix nanos.
	status      Status // The process last known status (new,young..dead).
	prevUpdTime uint64 // time at last sample.
	prevCpu     uint64 // total cpu used in jiffies at last sample
	updTime     uint64 // last update time as Unix nanos.
	stamp       tStamp // last stamp during which this process found
	pfnStat     string // "/proc/[pid]/stat"
	pfnStatus   string // "/proc/[pid]/status"
	pfnIo       string // "/proc/[pid]/io"
	cpu         uint64 // total cpu used jiffies at updtime.         // user time
	cmd         string
	exe         string
	cmdLine     string // \0 are replaced by ´ ´ for optimization (I know we loose some information for args with embedded spaces)
	path        string
	cpuTs       tStamp  // last upadte of cpu metric. TODO merge with staTs?
	cpupc       float32 // cpu usage percent
	statTs      tStamp  // Last update based on content of status file
	rss         uint64
	vsz         uint64
	threadNb    uint32
	statusTs    tStamp // Last update based on content of status file
	uid         int32
	gid         int32
	swap        uint64
	user        string
	group       string
	fdNbTs      tStamp
	fdNb        uint32
	ioTs        tStamp
	prevIo      uint64
	prevIoTime  time.Time
	io          uint64
	ioTime      time.Time
	vars        map[string]string // Synthetized variables (see revar filter)
}

func (p *procStat) tracef(d int, format string, a ...interface{}) {
	if p.cmd != "cpustress" {
		return
	}
	tracec(2, d, format, a)
}

func (p *procStat) trace(d int) {
	if p.cmd != "cpustress" {
		return
	}
	tracec(2, d, "pid=%d cmd=%s s=%d statTs=%d\n", p.pid, p.cmd, p.status, p.statTs)
}

func (p *procStat) String() string {
	l := len(p.cmdLine)
	if l > 10 {
		l = 10
	}
	cl := p.cmdLine[:l]
	if !p.IsThread() {
		return fmt.Sprintf("-- pid=%d ppid=%d state=%d cpu=%f rss=%d cmd=%s cl=%s", p.pid, p.ppid, p.status, p.cpupc, p.rss, p.cmd, cl)
	} else { // thread
		return fmt.Sprintf("----- pid=%d ppid=%d tgid=%d state=%d cpu=%f rss=%d cmd=%s cl=%s", p.pid, p.ppid, p.tgid, p.status, p.cpupc, p.rss, p.cmd, cl)
	}
}

func init() {
}

// Get (from cache) the number of CPUs on this server.
func CpuCount() uint {
	return CpuNb
}

func (p *procStat) IsThread() bool {
	//trace("pid=%d statusTs=%d", p.pid, p.statusTs)
	if p.statusTs == 0 { // Read only once, tgid does not change.
		// Never read /proc/pid/status => we must init the tgid.
		p.updateFromStatus()
	}
	//trace("pid=%d thread=%t", p.pid, p.pid != p.tgid)
	return p.pid != p.tgid
}

func (p *procStat) PID() tPid {
	return p.pid
}

func (p *procStat) ProcessNumber() uint64 {
	if p.IsThread() {
		return 0
	}
	return 1
}

// CPU compute CPU percent used since the last telegraf sample  (100% means use all the cores). We use a fastpath to bypass the slow gopsutil methods
// No specific code to handle the hyperthreading case where a core is not a real core. So when we compute the total available juffies we assume 100% thread availability.
// This can be tricky because we have to take care of a lot of special cases. (process is dead without CPU counter uupdate, very short lived processes, ...)
func (p *procStat) CPU() (float32, error) {
	if p.cpuTs == stamp {
		return p.cpupc, nil
	}
	p.updateFromStat() // Refresh process CPU counter.
	// cpu is counted as jiffies that are CPU quantum of time allocated to the process.
	var jiffies uint64
	if p.cpu != 0 {
		if p.prevUpdTime != 0 {
			// We have a complete sample. (usual case for long lived processes_
			if p.cpu > p.prevCpu { // protect against an unsigned int isue if there is an inversion in counters.
				jiffies = p.cpu - p.prevCpu
			}
		} else if p.startTime > curProcFilter.prevSampleStart {
			// First sample for this process.
			// But the process started during this interval soe know that these jiffies belong to this sample.
			jiffies = p.cpu
		} /*else {
			// We don't know enough to assign these jiffies.
			// TODO we could try to guess a prorata using startime?
			jiffies = 0
		}*/
		// Clear these jiffies that are now accounted for.
		p.prevCpu = p.cpu
		p.cpu = 0
	} /*else {
	// No ungathered CPU counters (jiffies).
	if p.status != DEAD {
		// Process is really using 0 jiffies in this interval (and is still alive.)
		jiffies = 0
	} */ /* else {
			// DEAD but we may try to extraoplate from last sample cpu usage.
			if p.prevUpdTime != 0 {
				if p.deathTime >= p.prevUpdTime {
					jiffies = 0 // DEBUG
					//t := p.deathTime - p.prevUpdTime
					//jiffies = uint64(p.cpupc * JiffiesPerNs * float32(t))
				} // else 0 jiffies, TODO try a guess?
			} else {
				// Dead during first sample so we assign one jiffy but this is a very rough guesstimate.
				jiffies = 0 // = 1 TODO debug
			}
		}
	}*/
	cpupc := float32(100*jiffies) / (curProcFilter.sampleDurationS * JiffiesPerS)

	// Quick fix until the >100% bug is found.
	if cpupc > 100 {
		if Debug != 0 {
			fmt.Printf("-- procstat > 100: cpu=%f jiff=%d %s\n", cpupc, jiffies, p.String())
		}
		// On some [flush] thread we get an enormous cpu value.
		cpupc = 0
	}
	p.prevUpdTime = p.updTime
	p.cpupc = cpupc
	p.cpuTs = stamp
	return cpupc, nil
}

func (p *procStat) fillIO() {
	if p.ioTs == stamp {
		return
	}
	p.ioTs = stamp
	ioLast := p.io // Store the previous sample IO counter value.
	p.updateFromIO()
	if p.status == DEAD {
		return
	}
	p.prevIo = ioLast
	p.prevIoTime = p.ioTime
	p.ioTime = time.Now()
	return
}

func (p *procStat) IO() (uint64, error) {
	p.fillIO()
	if p.prevIo == 0 {
		// First tick or no IO at all, we need 2 ticks to compute a delta.
		return 0, nil
	}
	dio := p.io - p.prevIo
	return dio, nil
}

func (p *procStat) IObps() (uint64, error) {
	p.fillIO()
	if p.prevIo == 0 {
		return 0, nil
	}
	dt := p.ioTime.Sub(p.prevIoTime).Seconds()
	var dio uint64
	if p.io > p.prevIo {
		dio = p.io - p.prevIo
	}
	iobps := uint64(float64(dio) / dt)
	return iobps, nil
}

func (p *procStat) GID() (int32, error) {
	if p.statusTs != 0 {
		return p.gid, nil
	}
	p.updateFromStatus()
	return p.gid, nil
}

func (p *procStat) UID() (int32, error) {
	if p.statusTs != 0 {
		return p.uid, nil
	}
	p.updateFromStatus()
	return p.uid, nil
}

func (p *procStat) Group() (string, error) {
	if p.group != "" {
		return p.group, nil
	}
	gid, _ := p.GID()
	g := GIDtoName(gid)
	p.group = g
	return g, nil
}

func (p *procStat) User() (string, error) {
	if p.user != "" {
		return p.user, nil
	}
	uid, _ := p.UID()
	u := UIDtoName(uid)
	p.user = u
	return u, nil
}

// The short executable name (without path)
func (p *procStat) Cmd() (string, error) {
	// cmd is always initialized during ps creation.
	return p.cmd, nil
}

// The full executable name (with path).
func (p *procStat) Exe() (string, error) {
	if p.exe != "" {
		return p.exe, nil
	}
	if p.cmdLine == "" { // the chdmline file has never been read.
		p.updateFromCmdline() // This will fill the exe field.
	}
	if p.exe == "" {
		p.exe = shortLivedString // Too late to get the value? Use a constant.
	}
	return p.exe, nil
}

// The full command line with args (using ´ ´ as separators).
func (p *procStat) CmdLine() (string, error) {
	if p.cmdLine == "" {
		p.updateFromCmdline()
	}
	return p.cmdLine, nil
}

func (p *procStat) Path() (string, error) {
	if p.path != "" {
		return p.path, nil
	}
	exe, err := p.Exe()
	if err == nil {
		p.path = filepath.Dir(exe)
	}
	if p.path == "" {
		p.path = shortLivedString
	}
	return p.path, err
}

func (p *procStat) RSS() (uint64, error) {
	if p.IsThread() {
		return 0, nil
	}
	if p.statTs == stamp {
		return p.rss, nil
	}
	p.updateFromStat()
	return p.rss, nil
}

func (p *procStat) VSZ() (uint64, error) {
	if p.IsThread() {
		return 0, nil
	}
	if p.statTs == stamp {
		return p.vsz, nil
	}
	p.updateFromStat()
	return p.vsz, nil
}

func (p *procStat) Swap() (uint64, error) {
	if p.IsThread() {
		return 0, nil
	}
	if p.statTs == stamp {
		return p.swap, nil
	}
	p.updateFromStatus()
	return p.swap, nil
}

func (p *procStat) ThreadNumber() (uint64, error) {
	if p.IsThread() {
		return 1, nil
	}
	if p.statTs == stamp {
		return uint64(p.threadNb), nil
	}
	p.updateFromStat()
	return uint64(p.threadNb), nil
}

func (p *procStat) FDNumber() (uint64, error) {
	if p.status == DEAD || p.IsThread() {
		return 0, nil
	}
	if p.fdNbTs == stamp {
		return uint64(p.fdNb), nil
	}
	p.fdNbTs = stamp
	err := p.updateFromFd()
	return uint64(p.fdNb), err
}

func (p *procStat) ChildrenPIDs(depth int) []tPid {
	tree := map[tPid]*procStat{}
	roots := []*procStat{}
	roots[0] = p
	getChildren(depth, roots, tree)
	children := make([]tPid, 0, len(tree))
	var i int
	for pid, _ := range tree {
		children[i] = pid
		i++
	}
	return children
}

// Fill All with (new) childrens of roots upto depth.
func getChildren(depth int, roots []*procStat, tree map[tPid]*procStat) {
	for _, ps := range roots {
		if _, known := tree[ps.pid]; known {
			continue // This pid is already in the tree and has been used as a search root.
		}
		// Insert this root in the tree
		tree[ps.pid] = ps
	}
	if depth <= 0 {
		return
	}
	// Search all processes if they have a parent in the tree.
	// TODO OPTIM use a smaller map containing only the PIDs at depth -1?
	newroots := []*procStat{}
	for _, aps := range allProcStats {
		if _, found := tree[aps.ppid]; found {
			// This process has a parent in the tree.
			newroots = append(newroots, aps)
		}
	}
	if len(newroots) != 0 {
		getChildren(depth-1, newroots, tree)
	}
}

func (p *procStat) Var(name string) string {
	val, known := p.vars[name]
	if known {
		return val
	} else {
		return ""
	}
}

func (p *procStat) PVars() *(map[string]string) {
	return &p.vars
}

// Switch status of this process to DEAD.
func (p *procStat) dead(ts uint64) {
	// Computing the most realistic time of death for a process may help find its CPU usage during its last uncomplete sample.
	if ts != 0 { // We probably got the real death time from a kenel exit event.
		p.deathTime = ts
	}
	if p.status != DEAD {
		p.status = DEAD
		if ts == 0 && p.deathTime == 0 {
			if p.updTime != 0 {
				// No precise info about deathtime, assume the deathtime was half the time since last we checked toe procees. (should be right on average)
				n := uint64(time.Now().UnixNano())
				p.deathTime = (p.updTime + n) / 2
			} else if p.startTime != 0 { // Least accurate case, assume the deathtime was half the time since process creation. (probably a very short lived process of a heavily loaded server)
				n := uint64(time.Now().UnixNano())
				// Try to get a time where the process was alive and take half the duration until now.
				at := p.startTime
				at = maxUint64(at, curProcFilter.prevSampleStart)
				p.deathTime = (at + n) / 2

			} // else we don't know enough to assume a deattime thus keep it at 0 (same as starttime)
		}
	}
	p.tracef(4, "pid=%d age=%d st=%d dt=%d", p.pid, p.deathTime-p.startTime, p.startTime, p.deathTime)
}

// Read /proc/ directory to get the current list of PIDs and add the new ones to the global PID->procstat map (and get a first sample for stats)
func scanPIDs(p *ProcFilter) error {
	trace("Scan of /proc")
	// Get all new proicesses
	d, err := os.Open("/proc/")
	if err != nil {
		return err
	}
	defer d.Close()
	fnames, err := d.Readdirnames(-1)
	if err != nil {
		return err
	}
	for _, fname := range fnames {
		if (fname[0] < '0') || (fname[0] > '9') {
			// Skip the conversion attempt if we know beforehand this is not a PID.
			continue
		}
		// Probably a PID.
		pid, err := strconv.ParseInt(fname, 10, 32)
		if err != nil {
			// If not numeric name then skip.
			continue
		}
		//trace("scanPIDs pid=%d", pid)
		addNewProcStat(tPid(pid), 0, true)
	}
	p.needOneScan = false // if asked for only one rescan for status reset, then it is done.
	return nil
}

// Given a PID create/init a new Procstat struct and add it to the global allProcstat map.
func addNewProcStat(pid tPid, ts uint64, lock bool) bool {
	if lock {
		apsMutex.Lock()
	}
	if _, known := allProcStats[pid]; known {
		// TODO check if this is the same process using starttime?
		if lock {
			apsMutex.Unlock()
		}
		trace("Already known, pid=%d\n", pid)
		return true
	}
	if lock {
		apsMutex.Unlock() // This function is called only during the state init phase. So we are quite sure we wont have someone else adding this PID until the next lock.
	}
	trace("new pid=%d stamp=%d\n", pid, stamp)

	s := procStat{}
	s.status = NEW
	s.pid = pid
	s.startTime = ts // Could be 0 if the information is missing (eg: not coming from a kernel event) or be an approximation (eg: coming from an exec event rather than a fork)
	s.pfnStat = procFileName(pid, "stat")
	s.uid = -1 // We may fail to read it properly.
	s.gid = -1
	if !s.initFromStat() {
		return false
	}

	//trace("aps done full: pid=%d %v\n", pid, s)
	if lock {
		apsMutex.Lock()
	}
	allProcStats[pid] = &s
	if lock {
		apsMutex.Unlock()
	}
	return true
}

// Inner function carved out of the loop to defer the mutex unlock. Compute time until next invocation to match Fast_interval pace.
func updateProcstatsHelper(p *ProcFilter) time.Duration {
	uar := curProcFilter.Update_age_ratio
	sd := curProcFilter.sampleDuration

	apsMutex.Lock()
	defer apsMutex.Unlock()
	//trace("wake\n")
	wake := time.Now()
	wakens := uint64(wake.UnixNano())
	for _, ps := range allProcStats {
		need := false
		switch ps.status {
		case NEW:
			// Transition from NEW to YOUNG at first update.
			ps.status = YOUNG
			need = true
		case YOUNG:
			dtu := wakens - ps.updTime // Time since last update, if no update has been done we get a very big dtu.
			// For young processes we check if we need to update them faster than the normal frequency.
			age := wakens - ps.startTime // age of the process.
			if dtu > uint64(float64(age)*uar) {
				// For young processes we update every time its last update is older thatn 10% of its age.
				need = true
			} else if uint64(float64(age)*uar) > sd {
				// This one is old enough to stop updating it at high frequency.
				ps.status = ADULT
			}
		case ADULT:
			// Stats will be updated durint the normal telegraf Gather()
		}
		if need {
			// Refresh process most useful informations.
			ps.updateFromStat()
		}
	}

	// How long must we sleep to be awaken at the requested Fast_interval freq? If the loop above was slow we need to speel less.
	now := time.Now()
	interval := p.Wakeup_interval // ms
	// delta between now and previous sample wakeup. (ms)
	d := int64(now.Sub(wake).Nanoseconds() / 1e6)
	s := interval - d // should sleep for s ms
	//trace("s= %d d=%d\n", s, d)

	// be sure to constrain the next sleep duration to prevent issues (eg: if clock changes)
	if s < 0 {
		s = interval / 2
	} else if s > 2*interval {
		s = 3 * interval / 2
	}
	//trace("sleep %dms\n", s)
	return time.Duration(s) * time.Millisecond
}

// Update informations about a subset of current processes. The choice is based on process age and last update time.
// This function is looping at a high frequency (Fast_interval) to get stats about short lived processes.
func updateProcstats(p *ProcFilter) {
	// Update stats then sleep a while.
	for {
		s := updateProcstatsHelper(p)
		time.Sleep(s) // s is the time to sleep to match the requested Fast_itnerval configuration value.
	}
}

// Rebuild the global allStats map. This is done only once per Gather() so the cost of this copy is acceptable.
func rebuildAllStats() error {
	// TODO optimize to avoid realloc?
	allStats = map[tPid]stat{}
	apsMutex.Lock()
	for pid, ps := range allProcStats {
		allStats[pid] = stat(ps)
	}
	apsMutex.Unlock()
	return nil
}

// Remove dead PIDs.
func clearOldProcStats() {
	dp := 0
	apsMutex.Lock()
	for pid, ps := range allProcStats {
		if ps.status == DEAD {
			delete(allProcStats, pid)
			dp++
		}
	}
	apsMutex.Unlock()
	if dp > 0 {
		trace("removed %d dead processes.", dp)
	}
}

// apsStats build a string with some stats about the global allProcStat.
func apsStats() string {
	apsMutex.Lock()
	defer apsMutex.Unlock()
	var cn, cy, ca, cd, cu int
	for _, ps := range allProcStats {
		switch ps.status {
		case NEW:
			cn++
		case YOUNG:
			cy++
		case ADULT:
			ca++
		case DEAD:
			cd++
		default:
			cu++
		}
	}
	if cu != 0 {
		logErr(fmt.Sprintf("%d processes with unknown status", cu))
	}
	return fmt.Sprintf("aps: all=%d new=%d young=%d adult=%d dead=%d", len(allProcStats), cn, cy, ca, cd)
}

func apsDisplay() {
	apsMutex.Lock()
	defer apsMutex.Unlock()
	fmt.Printf("All proc stats: len=%d\n", len(allProcStats))
	for _, ps := range allProcStats {
		fmt.Println(ps.String())
	}
	fmt.Printf("End All proc stats: len=%d\n", len(allProcStats))
}
