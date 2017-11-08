package procfilter

/*
#include "procevents.c"
*/
import "C"

import (
	"fmt"

	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/host"
)

var ClockTicks uint = 100  // Default value but a syscall will update it.
var PageSize uint64 = 4096 // Default value but a syscall will update it.
var CpuNb uint             // Number of CPUs(cores) on this server. Set during init().
var BootTimeNs uint64      // Time of system boot (ns since epoch)
var JiffiesPerS float32    // Number of available Jiffies each second for all the cores aggregated (a jiffie is the Linux CPU usage accounting uint.)
var JiffiesPerNs float32   // see JiffiesPerS but by nano seconds.

func init() {
	// Replace default values with values found on this server. (Should be the same but better safe than sorry)
	ClockTicks = uint(C.sysconf(C._SC_CLK_TCK))
	PageSize = uint64(C.sysconf(C._SC_PAGESIZE))
	i, _ := cpu.Counts(false)
	CpuNb = uint(i)
	bootTime, _ := host.BootTime() // in seconds since epoch
	BootTimeNs = bootTime * 1e9
	JiffiesPerS = float32(CpuNb * ClockTicks)
	JiffiesPerNs = JiffiesPerS / 1e9
	trace("jps=%f cpunb=%d", JiffiesPerS, CpuNb)
}

//export goProcEventFork
// This method is not called anymore. (see procevent.c for the rational)
func goProcEventFork(ppid, pid C.int, ts C.ulong) {
	// Most of the time forks are immediatly followed an exec .
	// We try to avoid the redundant acces to the /proc/[pid]/ files by defering the fork handling.
	// Forked processes that stay as a real fork are long lived anyway (eg: servers)
	trace("Fork: pid=%d ppid=%d ts=%d", pid, ppid, ts)
	forksMutex.Lock()
	forks[tPid(pid)] = uint64(ts)
	forksMutex.Unlock()
}

//export goProcEventExec
func goProcEventExec(pid C.int, ts C.ulong) {
	trace("Exec: pid=%d ts=%d", pid, ts)
	apsMutex.Lock()
	ps, known := allProcStats[tPid(pid)]
	if !known {
		addNewProcStat(tPid(pid), uint64(ts), false)
		apsMutex.Unlock()
	} else {
		// This should not happen. (the handling of fork events is defered so the exec event should be the first for a given PID)
		apsMutex.Unlock()
		trace("Exec already known process? pid=%d old_cmd=%s", pid, ps.cmd)
		ps.cmdLine = ""   // reset the cmdline (changed by exec)
		ps.initFromStat() // reset cmd and other stat related fields.
		trace("Exec pid=%d new_cmd=%s", pid, ps.cmd)
	}
	// Clean any corresponding (defered) fork event.+++
	forksMutex.Lock()
	delete(forks, tPid(pid))
	forksMutex.Unlock()
}

//export goProcEventExit
func goProcEventExit(cpid C.int, ts C.ulong) {
	pid := tPid(cpid)
	trace("Exit: pid=%d ts=%d", pid, ts)
	apsMutex.Lock()
	forksMutex.Lock()
	if ps, known := allProcStats[pid]; known {
		// This process is in the global procstat map, flag it dead with the proper timestamp.
		ps.dead(uint64(ts))
		delete(forks, pid) // Make sure it is not in the delayed fork map.
	} else {
		if sts, known := forks[pid]; known {
			// This process is in the defered fork map that is why it is not yet in allProcStats.
			// Create a procstat and mark it dead with the proper start/death time stamps (sts is the fork event time stamp.)
			s := procStat{}
			s.pid = pid
			s.status = DEAD
			s.startTime = sts
			s.deathTime = uint64(ts)
			allProcStats[pid] = &s
			delete(forks, pid)
		}
	}
	forksMutex.Unlock()
	apsMutex.Unlock()
}

//export goNeedOneScan
func goNeedOneScan() {
	// Next gather loop will do a full rescan of /proc to reset the state of the PIDs.
	if !curProcFilter.needOneScan {
		trace("Will do a rescan of /proc")
		curProcFilter.needOneScan = true
	}
}

// Get process events directly from the Linux kernel (via tne netlink. No lag, no missed events, ... Far superior to any scan based algorithm but not portable.
func getProcEvents(p *ProcFilter) {
	// This C function will connect to the kernel and wait for all events.
	// Events will be handled by callbacks in go. (see goProcEvent* functions above(.
	p.netlinkOk = true
	cr := C.getProcEvents() // This call will not return unless an error occurs (loop on select)
	if cr == -1 {
		cmsg := C.GoString(C.errMsg())
		msg := fmt.Sprintf("Unable to set the Netlink socket properly (%s)", cmsg)
		logErr(msg)
		p.netlinkOk = false
	}
}
