package procfilter

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/user"
	"path"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var traceOn = true

func tron() {
	traceOn = true
}

func troff() {
	traceOn = false
}

// callers returns information about some of the calling functions (from a given stack lvl)
// ex: callers(1,1) returns the file,line,name of parent function
func traceCallers(from int, nb int) string {
	s := ""
	pc := make([]uintptr, 50)
	pcs := runtime.Callers(1+from, pc)
	if pcs < nb {
		nb = pcs
	}
	lvl := from + nb - 1
	if lvl < 0 {
		lvl = 0
	} else if lvl >= pcs {
		lvl = pcs - 1
	}
	for i := 0; i < nb-1; i++ {
		f := runtime.FuncForPC(pc[lvl])
		file, line := f.FileLine(pc[lvl])
		base := path.Base(file)
		bf := path.Base(f.Name())
		if lvl == from {
			nm := (i - 1) * 2
			if nm < 0 {
				nm = 0
			}
			s = fmt.Sprintf("%s\n%s->%s:%d %s:", s, strings.Repeat("-", nm), base, line, bf)
		} else {
			s = fmt.Sprintf("%s\n%s%s:%d %s:", s, strings.Repeat("-", i*2), base, line, bf)
		}
		lvl--
	}
	return s
}

// tracec like trace but with a deeper stack trace.
func tracec(from int, nb int, format string, a ...interface{}) {
	return
	if traceOn == false {
		return
	}
	ci := traceCallers(from, nb)
	m := fmt.Sprintf(format, a...)
	if m[len(m)-1] == '\n' { // Already terminated with a \n.
		fmt.Fprintf(os.Stderr, "%s:%s", ci, m)
	} else {
		fmt.Fprintf(os.Stderr, "%s:%s\n", ci, m)
	}
}

// trace helper during debug.
func trace(format string, a ...interface{}) {
	return
	if traceOn == false {
		return
	}
	ci := traceCallers(1, 1)
	m := fmt.Sprintf(format, a...)
	if m[len(m)-1] == '\n' { // Already terminated with a \n.
		fmt.Fprintf(os.Stderr, "%s:%s", ci, m)
	} else {
		fmt.Fprintf(os.Stderr, "%s:%s\n", ci, m)
	}
}

/* A dual string/regexp object
A r suffix denotes a regular expression rather than a plain string.
'fu' matches exactly fu
'fu'r matches anything containing fu
'^fu'r matches anything starting with fu
*/
type stregexp struct {
	isRe   bool
	invert bool // Invert the match.
	pat    string
	re     *regexp.Regexp // the compiled version of the user string
}

func NewStregexp(pat string, isRe bool, invert bool) (*stregexp, error) {
	var sr stregexp

	if isRe {
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, err
		}
		sr.re = re
	}
	sr.isRe = isRe
	sr.invert = invert
	sr.pat = pat
	return &sr, nil
}

func (sr *stregexp) matchString(s string) bool {
	if !sr.isRe {
		// plain string compare
		return sr.invert != (sr.pat == s) // booleas XOR
	}
	// regexp match
	return sr.invert != sr.re.MatchString(s) // booleas XOR
}

var g2nCache = map[int32]string{}

func GIDtoName(gid int32) string {
	if g, known := g2nCache[gid]; known {
		return g
	} else {
		gpugroup, err := user.LookupGroupId(strconv.Itoa(int(gid)))
		if err == nil {
			g = gpugroup.Name
			g2nCache[gid] = g
			return g
		}
	}
	g := strIdUnknown
	g2nCache[gid] = g
	return g
}

var u2nCache = map[int32]string{}

func UIDtoName(uid int32) string {
	if u, known := u2nCache[uid]; known {
		return u
	} else {
		gpuuser, err := user.LookupId(strconv.Itoa(int(uid)))
		if err == nil {
			u = gpuuser.Username
			u2nCache[uid] = u
			return u
		}
	}
	u := strIdUnknown
	u2nCache[uid] = u
	return u
}

func NametoUID(name string) (int32, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return -1, err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return -1, err
	}
	return int32(uid), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func logErr(msg string) {
	log.Printf("E! procfilter: %s", msg)
}

func logWarning(msg string) {
	log.Printf("W! procfilter: %s", msg)
}
func logInfo(msg string) {
	log.Printf("I! procfilter: %s", msg)
}

func NYIError(msg string) error {
	return fmt.Errorf("%s not yet implemented", msg)
}

// pidFromFile read a PID from a file.
func pidFromFile(file string) (tPid, error) {
	pidString, err := ioutil.ReadFile(file)
	if err != nil {
		return 0, fmt.Errorf("cannot get PID stored in file '%s', %s", file, err.Error())
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidString)))
	if err != nil {
		return 0, fmt.Errorf("cannot get PID stored in file '%s', %s", file, err.Error())
	}
	return tPid(pid), nil
}

func fileContent(file string) (string, error) {
	scriptString, err := ioutil.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("cannot get the script stored in file '%s', %s", file, err.Error())
	}
	return string(scriptString), nil
}

// applyAll calls apply for all filters.
func applyAll(filters []filter) error {
	for _, f := range filters {
		err := f.Apply()
		if err != nil {
			return err
		}
	}
	return nil
}

// unpackSliceAsMap convert a slice of stats to an unpacked map of pid=>*procStat.If sm is not nil, add to this map.
func unpackSliceAsMap(stats []stat, sm map[tPid]*procStat) map[tPid]*procStat {
	if sm == nil {
		sm = map[tPid]*procStat{}
	}
	for _, s := range stats {
		if s == nil {
			continue
		}
		id := s.PID()
		if id >= 0 {
			// A procStat
			sm[id] = s.(*procStat)
		} else {
			// This is a packed stat, need to unpack
			ps := s.(*packStat)
			if ps.other != "" {
				continue
			}
			for _, ss := range ps.elems {
				pid := ss.PID()
				sm[pid] = s.(*procStat)
			}
		}
	}
	return sm
}

// unpackSliceAsSlice convert a slice of stats to an unpacked slice of *procStat.. If ss is not nil, append to ss.
func unpackSliceAsSlice(stats []stat, ss []*procStat) []*procStat {
	if ss == nil {
		ss = []*procStat{}
	}
	for _, s := range stats {
		if s == nil {
			continue
		}
		id := s.PID()
		if id >= 0 {
			// A procStat
			ss = append(ss, s.(*procStat))
		} else {
			// This is a packed stat, need to unpack
			ps := s.(*packStat)
			if ps.other != "" {
				continue
			}
			for _, subs := range ps.elems {
				ss = append(ss, subs)
			}
		}
	}
	return ss
}

// unpackMapAsMap convert a map of pid=>stats to an unpacked map of pid=>*procStat. If sm is not nil, add to this map.
func unpackMapAsMap(stats map[tPid]stat, sm map[tPid]*procStat) map[tPid]*procStat {
	if sm == nil {
		sm = map[tPid]*procStat{}
	}
	for id, s := range stats {
		if s == nil {
			continue
		}
		if id >= 0 {
			// A procStat
			sm[id] = s.(*procStat)
		} else {
			// This is a packed stat, need to unpack
			ps := s.(*packStat)
			if ps.other != "" {
				continue
			}
			for _, ss := range ps.elems {
				pid := ss.PID()
				sm[pid] = ss
			}
		}
	}
	return sm
}

// unpackMapAsSlice convert a map of pid=>stats to an unpacked slice of *procStat. If ss is not nil, append to ss.
func unpackMapAsSlice(stats map[tPid]stat, ss []*procStat) []*procStat {
	if ss == nil {
		ss = []*procStat{}
	}
	for id, s := range stats {
		if s == nil {
			continue
		}
		if s == nil {
			continue
		}
		if id >= 0 {
			// A procStat
			ss = append(ss, s.(*procStat))
		} else {
			// This is a packed stat, need to unpack
			ps := s.(*packStat)
			if ps.other != "" {
				continue
			}
			for _, subs := range ps.elems {
				ss = append(ss, subs)
			}
		}
	}
	return ss
}

// unpackStatAsMap unpack a stat to an unpacked map of pid=>*procStat.If sm is not nil, add to this map.
func unpackStatAsMap(s stat, sm map[tPid]*procStat) map[tPid]*procStat {
	if sm == nil {
		sm = map[tPid]*procStat{}
	}
	id := s.PID()
	if id >= 0 {
		// A procStat
		sm[id] = s.(*procStat)
	} else {
		// This is a packed stat, need to unpack
		ps := s.(*packStat)
		if ps.other != "" {
			return sm
		}
		for _, ss := range ps.elems {
			pid := ss.PID()
			sm[pid] = s.(*procStat)
		}
	}
	return sm
}

// unpackStatAsSlice unpack a stat to a slice of *procStat. If ss is not nil, append to ss.
func unpackStatAsSlice(s stat, ss []*procStat) []*procStat {
	if ss == nil {
		ss = []*procStat{}
	}
	id := s.PID()
	if id >= 0 {
		// A procStat
		ss = append(ss, s.(*procStat))
	} else {
		// This is a packed stat, need to unpack
		ps := s.(*packStat)
		if ps.other != "" {
			return ss
		}
		for _, subs := range ps.elems {
			ss = append(ss, subs)
		}
	}
	return ss
}

func unpackFiltersAsMap(filters []filter, sm map[tPid]*procStat) map[tPid]*procStat {
	if sm == nil {
		sm = map[tPid]*procStat{}
	}
	for _, f := range filters {
		sm = f.Stats().unpackAsMap(sm)
	}
	return sm
}

func unpackFiltersAsSlice(filters []filter, ss []*procStat) []*procStat {
	if ss == nil {
		// TODO if all stats are procstats return the original slice rather than building a new identical one?
		ss = []*procStat{}
	}
	// We use an intermediate map to be sure that no *procStat is twice in the final slice.
	sm := unpackFiltersAsMap(filters, nil)
	for _, ps := range sm {
		ss = append(ss, ps)
	}
	return ss
}

func findNextIndex(s []byte, start int, char byte) int {
	sl := len(s)
	for ; start < sl; start++ {
		if s[start] == char {
			return start
		}
	}
	return sl
}

// Build a string containing the script with line numbers and optional error markers.
func debugScript(s string, eln, ecn int) string {
	scanner := bufio.NewScanner(strings.NewReader(s))
	var ds string
	var ln int
	for scanner.Scan() {
		ln++
		if eln != ln {
			ds += fmt.Sprintf("%3d : %s\n", ln, scanner.Text())
		} else {
			ds += fmt.Sprintf("%3d>: %s\n", ln, scanner.Text())
			var ca string
			for c := 0; c < ecn; c++ {
				ca += " "
			}
			ca += "^"
			ds += fmt.Sprintf(">>>:%s\n", ca)
		}
	}
	return ds
}
