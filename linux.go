package procfilter

/* Linux specific code. Some of the gopsutils methods are too slow and this file contains their "fast" (upto 40x) counterparts.
(The design is not similar enough for a merge with the original lib).
WARNING: Some of the functions in this file are designed to be fast but have strict requirements for their safe usage. eg: fastRead() fastReadLine() ... Please read carefully the instructions.
*/
import _ "net/http/pprof" // TODO debug only

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"time"
)

const ktCmdLine = "[kernel]" // Use this as a command line for kernel threads.
const frBufferSize = 4096

var frBuffer [frBufferSize]byte // Used as temp storage for content of files in /proc/[PID]/stat* or cmdline (sampling on our servers indicates <400 bytes).
var frLineBuffer [1024]byte     // Used as temp storage for a line content (fastReadLine)
var rli int                     // Position of next char in readBuffer
var rll int                     // number of real bytes in readLineBuffer (near end of file we may get a parlialy filled buffer)
var frFile *os.File             // File handle for fastReadLine.

// fastReadOpen opens a file for fastReadLine.
func fastReadOpen(fn string) error {
	if frFile != nil {
		frFile.Close()
	}
	rli = 0
	rll = 0
	var err error
	frFile, err = os.Open(fn)
	if err != nil {
		//trace("open err=%s", err)
		return err
	}
	return nil
}

// fastReadClose close the current file for fastRead*.
// Note that if you read files until EOF, then calling this function is not required.
func fastReadClose() {
	if frFile != nil {
		frFile.Close()
		frFile = nil
	}
	rli = 0
	rll = 0
}

// fastReadByte reads one character from a file using a single static common buffer.
// WARNING: You must call fastReadOpen first.
// WARNING: Designed to be fast but has no guards against concurrent use. (single common  buffer with no mutex)
// WARNING: you must check for err!=nil every
func fastReadByte() (byte, error) {
	if rli >= rll { // no more chars in the read buffer.
		rll, _ = frFile.Read(frBuffer[:]) // Refill the read buffer
		// Do not consider a partial read as a real error..
		if rll == 0 { // Error or EOF
			rli = 0
			frFile.Close()
			frFile = nil
			return 0, os.ErrClosed
		}
		rli = 0
	}
	b := frBuffer[rli]
	rli++
	return b, nil
}

// fastReadLine reads a line from a file uing a single static common buffer.
// WARNING: You must call fastReadOpen first.
// WARNING: Designed to be fast but has no guards against concurrent use or big lines buffer overflow. (single common small buffer with no mutex)
// WARNING: shares low level buffer with fastRead. So you cannot use both functions at the same time.
func fastReadLine() []byte {
	if frFile == nil { // Should call fastReadLineOpen()
		return nil
	}

	var line = frLineBuffer[:0] // empty slice using the line buffer as backing array.
	bi := rli
	for {
		if bi >= rll { // no more chars in the read buffer.
			if rll > 0 {
				line = append(line, frBuffer[rli:rll]...) // Keep the chunk of line that is at the end of the current read buffer.
			}
			rll, _ = frFile.Read(frBuffer[:]) // And refill the read buffer
			// Do not consider a partial read as a real error..
			if rll == 0 { // Error or EOF
				rli = 0
				frFile.Close()
				frFile = nil
				if len(line) != 0 {
					return line
				} else {
					return nil
				}
			}
			bi = 0
			rli = 0
		}
		if frBuffer[bi] == '\n' {
			line = append(line, frBuffer[rli:bi]...)
			rli = bi + 1
			return line
		}
		bi++
	}
	return nil
}

// fastRead reads a short file using a single static common buffer.
// WARNING: Designed to be fast but has no guards against concurrent use or big files buffer overflow. (single common small buffer with no mutex)
func fastRead(fn string) ([]byte, error) {
	//traceCaller(3, "fr: %s", fn)
	f, err := os.Open(fn)
	if err != nil {
		//trace("open err=%s", err)
		return nil, err
	}
	n, err := f.Read(frBuffer[:])
	f.Close()
	if err != nil && err != io.EOF {
		//trace("read err=%s", err)
		return nil, err
	}
	//fmt.Printf("fr: read ok: %s\n", fn)
	return frBuffer[0:n], nil
}

// procFileName create a string for file names in /proc/[PID]/[name]
func procFileName(pid tPid, name string) string {
	// Very naive/slow but is used twice per process only.
	// TODO optimize using []byte and copy?
	return fmt.Sprintf("/proc/%d/%s", pid, name)
}

// WARNING: to be fast this function assumes that we are on the first digit of the integer to parse.
func fastParseUint64(s []byte, i int) (res uint64, index int) {
	sl := len(s)
	for ; i < sl; i++ {
		if s[i] < '0' || '9' < s[i] {
			//fmt.Printf("res=%d i=%d c='%c':>>%s<<\n", res, i, s[i], s[i:])
			return res, i
		}
		// TODO bench and find faster algorithm? (precomputed tables?)
		res = 10*res + uint64(s[i]-'0')
	}
	return res, i
}

// Search s for the previous integer.
// Assumes we are scaning a file structured like /proc/[pid]/status where we have lines like: key : value.
// In this case we assume i is pointing between the EOL and last digit of the interger and value is an int followed by an optional unit. eg: VmSwap:	     384 kB
func fastParsePrevInt(s []byte, i int) (res int64) {
	m := int64(1)
	j := i
	for ; j >= 0; j-- { // search backward for a digit.
		c := s[j]
		switch {
		case '0' <= c && c <= '9':
			break
		case c == ':':
			return res // should not happen, only guards against anomalous file
		}
	}
	for ; j >= 0; j-- {
		c := s[i]
		if c < '0' || '9' < c {
			return res
		}
		res += m * int64(c-'0')
		m *= 10
	}
	return res
}

// fastParseUntil returns the string between current position and first occurence of delim (delim not included) or end of string. index points on the delim.
func fastParseUntil(s []byte, i int, delim byte) (res string, index int) {
	si := i
	sl := len(s)
	for ; i < sl; i++ {
		if s[i] == delim {
			return string(s[si:i]), i
		}
	}
	return string(s[si : i-1]), i
}

// Get the short name for a kernel thread. (get the prefix without / or any other non alpha chars). eg: ksoftirq/0 -> [ksoftirq]
func shortKernelCmd(s string) string {
	sl := len(s)
	for i := 1; i < sl; i++ {
		if (s[i] < 'a' || 'z' < s[i]) && (s[i] < '0' || '9' < s[i]) {
			// Not alpha numeric, keep only the head of the command and add enclosing [].
			return fmt.Sprintf("[%s]", s[0:i])
		}
	}
	return fmt.Sprintf("[%s]", s) // already OK, add enclosing [].
}

// The /proc/[PID]/stat file content is described in package kernel-doc
// this one is from: kernel-doc-2.6.32-642.el6.noarch
// /usr/share/doc/linux-doc/filesystems/proc.txt.gz
// eg: cat /proc/20411/stat|tr ' ' '\n' | tail -n +2 | nl
//
/*
   Table 1-4: Contents of the stat files (as of 2.6.30-rc7)
      Field         Content
     0    pid           process id
     1	  tcomm         filename of the executable
     2	  state         state (R is running, S is sleeping, D is sleeping in an uninterruptible wait, Z is zombie, T is traced or stopped)
     3	  ppid          process id of the parent process
     4	  pgrp          pgrp of the process
     5	  sid           session id
     6	  tty_nr        tty the process uses
     7	  tty_pgrp      pgrp of the tty
     8	  flags         task flags
     9	  min_flt       number of minor faults
    10	  cmin_flt      number of minor faults with child's
    11	  maj_flt       number of major faults
    12	  cmaj_flt      number of major faults with child's
    13	  utime         user mode jiffies
    14	  stime         kernel mode jiffies
    15	  cutime        user mode jiffies with child's
    16	  cstime        kernel mode jiffies with child's
    17	  priority      priority level
    18	  nice          nice level
    19	  num_threads   number of threads
    20	  it_real_value	(obsolete, always 0)
    21	  start_time    time the process started after system boot (in clock ticks!)
    22	  vsize         virtual memory size
    23	  rss           resident set memory size
    24	  rsslim        current limit in bytes on the rss
    25	  start_code    address above which program text can run
    26	  end_code      address below which program text can run
    27	  start_stack   address of the start of the stack
    28	  esp           current value of ESP
    29	  eip           current value of EIP
    30	  pending       bitmap of pending signals
    31	  blocked       bitmap of blocked signals
    32	  sigign        bitmap of ignored signals
    33	  sigcatch      bitmap of catched signals
    34	  wchan         address where process went to sleep
    35	  0             (place holder)
    36	  0             (place holder)
    37	  exit_signal   signal to send to parent thread on exit
    38	  task_cpu      which CPU the task is scheduled on
    39	  rt_priority   realtime priority
    40	  policy        scheduling policy (man sched_setscheduler)
    41	  blkio_ticks   time spent waiting for block IO
    42	  gtime         guest time of the task in jiffies
    43	  cgtime        guest time of the task children in jiffies
*/

// initStats must be called at least once per procstat. Using the content of the /proc/[pic]/stat file it inits and collects some metrics. (very similar to updateFromStat but parses more imutable fields (eg: starttime, cmd. ppid, ...). If the process is already dead retruend false.
func (ps *procStat) initFromStat() bool {
	s, err := fastRead(ps.pfnStat)
	sl := len(s)
	if err != nil || sl == 0 {
		ps.dead(0)
		return false // TODO we have very little information. do we keep it anyway?
	}
	// This is the procstat creation so we fill the "prev"  sample values to help get accurate stats on short lived processes.
	ps.updTime = uint64(time.Now().UnixNano())
	var f int      // field number (0 is pid)
	var res uint64 // holds the return value of fastParseInt
	var resstr string
	for i := 0; i < sl; i++ {
		//fmt.Printf("f:%d i:%d c:%c\n", f, i, s[i])
		switch f {
		case 1: // 1 tcomm
			i++ // Skip the '('.
			resstr, i = fastParseUntil(s, i, ')')
			ps.cmd = resstr
			//trace("pid=%d cmd=%s", ps.pid, ps.cmd)
		case 3: // 3 ppid
			res, i = fastParseUint64(s, i)
			ps.ppid = tPid(res)
		/*case 4: // 4 pgrp
		pgrp, i = fastParseInt(s, i) // used to detect kernel threads
		*/
		case 13: // utime is number of jiffies used by this process in user mode.
			res, i = fastParseUint64(s, i)
			ps.cpu = res
		case 14: // stime is number of jiffies used by this process in system mode.
			res, i = fastParseUint64(s, i)
			ps.cpu += res // aggregate sys+user jiffies.
		case 19: // num_threads   number of threads
			res, i = fastParseUint64(s, i)
			ps.threadNb = uint32(res)
		case 21: // start time in clock ticks since server boot
			res, i = fastParseUint64(s, i)
			ps.startTime = (res * 1e9 / uint64(ClockTicks)) + BootTimeNs // make starttime absolute (ns since epoch)
		case 22: // vsz
			res, i = fastParseUint64(s, i)
			ps.vsz = res
		case 23: // rss
			res, i = fastParseUint64(s, i)
			ps.rss = res * PageSize
			//ps.trace(4)
			return true // Last field we need to parse.
		default: // Skip this field.
			i++
			for ; i < sl; i++ {
				if s[i] == ' ' {
					break
				}
			}
		}
		// Assume one and only one ' '  between fields.
		f++
	}
	return false // Did not parse upto the last required field. This should not be possible but ...
}

// updateFromStat using the content of /proc/[pid]/stat, updates some stats about this process that will be used later (during Gather). The idea is to refresh some sttas that are considered critical if the process dies before the next Gather (eg: cpu usage).
func (ps *procStat) updateFromStat() error {
	ps.statTs = stamp
	if ps.status == DEAD {
		return nil
	}
	s, err := fastRead(ps.pfnStat)
	sl := len(s)
	if err != nil || sl == 0 {
		ps.dead(0)
		return err
	}
	ps.updTime = uint64(time.Now().UnixNano())
	var f int      // field number (0 is pid)
	var res uint64 // holds the return value of fastParseInt
	for i := 0; i < sl; i++ {
		switch f {
		case 13: // utime is number of jiffies used by this process in user mode.
			res, i = fastParseUint64(s, i)
			ps.cpu = res
		case 14: // stime is number of jiffies used by this process in system mode.
			res, i = fastParseUint64(s, i)
			ps.cpu += res // aggregate sys+user jiffies.
		case 19: // num_threads   number of threads
			res, i = fastParseUint64(s, i)
			ps.threadNb = uint32(res)
		case 22: // vsz
			res, i = fastParseUint64(s, i)
			ps.vsz = res
		case 23: // rss
			res, i = fastParseUint64(s, i)
			ps.rss = res * PageSize
			return nil // Last field we need to parse.
		default: // Skip this field.
			i++
			for ; i < sl; i++ {
				if s[i] == ' ' {
					break
				}
			}
		}
		// Assume one and only one ' '  between fields.
		f++
	}
	return nil
}

// Find some values (lswap, tgid, ...) found only in /proc/[pid]/status.
// The key: values change between kernel versions or process types (kernel workers vs process) so we have to search for the keys.
func (ps *procStat) updateFromStatus() {
	if ps.statusTs == stamp || ps.status == DEAD {
		return
	}
	ps.statusTs = stamp
	if ps.pfnStatus == "" {
		ps.pfnStatus = procFileName(ps.pid, "status")
	}
	s, err := fastRead(ps.pfnStatus)
	sl := len(s)
	if err != nil || sl == 0 {
		ps.dead(0)
		return
	}
	sol := true
	var i int
	var res uint64
	for {
		if sol {
			if i+5 >= sl {
				return // end of file.
			}
			sol = false
			// Start of line. So next bytes are the key.
			if ps.tgid == 0 && s[i] == 'T' && s[i+1] == 'g' && s[i+3] == 'd' && s[i+4] == ':' { // probably never parsed tgid and discriminate for Tgid: key
				//Tgid:	1520
				i += 5
				for ; i < sl; i++ {
					if '0' <= s[i] && s[i] <= '9' {
						res, i = fastParseUint64(s, i)
						ps.tgid = tPid(res)
						//trace("pid=%d tgid=%d", ps.pid, ps.tgid)
						break
					}
				}
			} else if s[i] == 'U' && s[i+1] == 'i' && s[i+2] == 'd' {
				//Uid:    1000    1000    1000    1000
				// 4 values: real, effective, saved, fs
				var id uint64
				i = i + 5
				id, i = fastParseUint64(s, i)
				ps.uid = int32(id)
			} else if s[i] == 'G' && s[i+1] == 'i' && s[i+2] == 'd' {
				//Gid:    1000    1000    1000    1000
				// 4 values: real, effective, saved, fs
				var id uint64
				i = i + 5
				id, i = fastParseUint64(s, i)
				ps.gid = int32(id)
			} else if s[i] == 'V' && s[i+2] == 'S' && s[i+5] == 'p' {
				// On RH<6 we don't have VmSwap.
				// VmSwap:  7234112 kB
				i += 7
				for ; i < sl; i++ {
					if '0' <= s[i] && s[i] <= '9' {
						res, i = fastParseUint64(s, i)
						ps.swap = res * 1024 // status contais the  swap in kB.
						//trace("pid=%d tgid=%d swap=%d", ps.pid, ps.tgid, ps.swap)
						return // swap is the last value we want to extract.}
					}
				}
			} else {
				i++
			}
		} else {
			i++
		}
		if i >= sl {
			return // end of file, some values were not found.
		}
		if s[i] == '\n' {
			i++
			if i >= sl {
				return // end of file, some values were not found.
			}
			sol = true
		}
	}
}

// PSS (Proportional Set Size) is available in /proc/[pid]/smaps.
// We have to summ all Pss: lines.
func (ps *procStat) updateFromSmaps() {
	// Verified with:
	//   cd /proc; for PID in [0-9]*; do PSS=$(echo $(egrep "^Pss:" /proc/$PID/smaps 2>/dev/null | egrep -o "[0-9]+" | sed -r 's/$/+/' | tr -d '\n')0 | bc); if [[ $PSS != 0 ]]; then echo "$PSS kB : $PID : $(cat /proc/$PID/cmdline)"; fi ; done | sort -n
	if ps.smapsTs == stamp || ps.status == DEAD {
		return
	}
	ps.smapsTs = stamp
	if ps.pfnSmaps == "" {
		ps.pfnSmaps = procFileName(ps.pid, "smaps")
	}
	err := fastReadOpen(ps.pfnSmaps)
	if err != nil {
		ps.dead(0)
		return
	}
	var totpss int
	var kw uint32 // The bytes from 'Pss:' can be stored on a single 32b word.
	for {
		c, err := fastReadByte()
		if err != nil {
			break
		}
		kw = (kw << 8) + uint32(c)
		if kw != 0x5073733a { // echo -n "Pss:" | od -t x4 --endian=big
			continue
		}
		kw = 0
		// We matched "Pss:", now search for the digits.
		var pss int = 0
	WSpaces:
		c, err = fastReadByte()
		if err != nil {
			break
		}
		if c == ' ' {
			goto WSpaces
		}
		var unread = true
	Digits:
		if !unread {
			c, err = fastReadByte()
			if err != nil {
				break
			}
		} else {
			unread = false
		}
		if (c >= '0') && (c <= '9') {
			pss = pss*10 + int(c-'0')
			goto Digits
		}
		totpss += pss
		// skip the "kB: sctring.
		_, _ = fastReadByte()
		_, _ = fastReadByte()
	}
	ps.pss = uint64(totpss) << 10 // kB to bytes <=> *1024 <=> left shift 10
}

// Init cmdline from the content of /proc/[pid]/cmdline. If cmdline is empty assume this is a kernel thread and fix its cmd value to remove all CPU related info.
func (ps *procStat) updateFromCmdline() error {
	if ps.status == DEAD {
		ps.cmdLine = shortLivedString
		return nil
	}
	// TODO use a static buffer to avoid gc?
	s, err := ioutil.ReadFile(procFileName(ps.pid, "cmdline"))
	//trace("cmdline: %d '%s'", len(s), s)
	if err != nil {
		ps.cmdLine = shortLivedString
		ps.dead(0)
		return err
	}
	sl := len(s)
	if sl == 0 {
		// kernel thread. Rework its name to make it easier to aggregate later.
		ps.cmd = shortKernelCmd(ps.cmd)
		//trace("kernel thread pid=%d new cmd=%s", ps.pid, ps.cmd)
		ps.cmdLine = ktCmdLine
	} else {
		// Fill the exe field before loosing the \0 marker.
		ee := findNextIndex(s, 0, 0)
		for i := 0; i < sl; i++ {
			if s[i] == 0 {
				s[i] = ' ' // replace the \0 separating args by a plain whitespace. We do not use the fine arg by arg struct of argv[]
			}
		}
		sl-- // The last char is a 0, skip its copy.
		ps.cmdLine = string(s)
		ps.exe = ps.cmdLine[0:ee] // TODO This assumes that we have one char per byte... fishy if not utf8/ascii...
	}
	return nil
}

// updateFromFd using the content of /proc/[pid]/fd/, updates the number of open files.
func (ps *procStat) updateFromFd() error {
	if ps.status == DEAD {
		ps.fdNb = 0
		return nil
	}
	path := procFileName(ps.pid, "fd")
	d, err := os.Open(path)
	if err != nil {
		ps.fdNb = 0
		// We cannot assume that the process is dead, maybe some access permssion issues?
		return err
	}
	defer d.Close()
	fi, err := d.Readdir(0)
	if err != nil {
		ps.fdNb = 0
		// We cannot assume that the process is dead, maybe some access permssion issues?
		return err
	}
	ps.fdNb = uint32(len(fi))
	return nil
}

// Get summ of IO Read/Write counters from /proc/#/io
func (ps *procStat) updateFromIO() {
	if ps.pfnIo == "" {
		ps.pfnIo = procFileName(ps.pid, "io")
	}
	s, err := fastRead(ps.pfnIo)
	sl := len(s)
	if err != nil || sl == 0 {
		ps.dead(0)
		return
	}
	sol := true
	var i int
	var io uint64
	for {
		if sol {
			if i+6 >= sl {
				return // end of file.
			}
			// Doc in https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/tree/Documentation/filesystems/proc.txt?id=HEAD#l1305
			// use these 2 metrics rather that rchar/wchar to track disk level activity (avoid counting memory cache accesses)
			// read_bytes: 809781143552
			// write_bytes: 4366930132992

			sol = false
			// Start of line. So next bytes are the key.
			if s[i] == 'r' && s[i+1] == 'e' && s[i+5] == 'b' {
				// read_bytes: 809781143552
				i += 12
				io, i = fastParseUint64(s, i)
				//fmt.Printf("pid=%d ior=%d\n", ps.pid, io)
			} else if s[i] == 'w' && s[i+1] == 'r' && s[i+6] == 'b' {
				// write_bytes: 4366930132992
				var r uint64
				i += 13
				r, i = fastParseUint64(s, i)
				//fmt.Printf("pid=%d ior=%d iow=%d\n", ps.pid, r, io)
				ps.io = io + r
				return
			} else {
				i++
			}
		} else {
			i++
		}
		if i >= sl {
			return // end of file, swap not found.
		}
		if s[i] == '\n' {
			i++
			if i >= sl {
				return // end of file, swap not found.
			}
			sol = true
		}
	}
	return
}
