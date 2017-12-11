package procfilter

import (
	"fmt"
)

var packStatId tPid
var errNoID error

func init() {
	errNoID = fmt.Errorf("this packStat has no ID.")
}

// resetAllGatherStats  prepare pack stats for a new sample.
func resetAllPackStats() {
	packStatId = 0
}

func NewPackStat(es []*procStat) *packStat {
	packStatId--
	s := packStat{pid: packStatId, elems: es}
	s.uid = -1 // flag as not initialized
	s.gid = -1
	return &s
}

/* Packed/aggregated stat for a set of stat (probably mainly procStats) as a single entity
This is used to gather a single value for a set of processes. Eg: The total RSS used by all tomcat processes
*/
type packStat struct {
	pid   tPid
	elems []*procStat
	other string // Special "other" case
	// pack criteria values
	uid int32
	gid int32
	cmd string
	// aggregated values
	procNbTs   tStamp
	procNb     uint64
	threadNbTs tStamp
	threadNb   uint64
	fdNbTs     tStamp
	fdNb       uint64
	cpuTs      tStamp
	cpu        float32
	ioTs       tStamp
	io         uint64
	iobpsTs    tStamp
	iobps      uint64
	rssTs      tStamp
	rss        uint64
	vszTs      tStamp
	vsz        uint64
	swapTs     tStamp
	swap       uint64
	vars       map[string]string
}

func (p *packStat) PID() tPid {
	return p.pid
}

func (p *packStat) ProcessNumber() uint64 {
	if p.procNbTs == stamp {
		return p.procNb
	}
	var nb uint64
	for _, s := range p.elems {
		nb += s.ProcessNumber()
	}
	p.procNb = nb
	p.procNbTs = stamp
	return nb
}

func (p *packStat) Args() ([]string, error) {
	return []string{}, nil
}

func (p *packStat) CPU() (float32, error) {
	if p.cpuTs == stamp {
		return p.cpu, nil
	}
	var cpu float64
	for _, s := range p.elems {
		c, _ := s.CPU()
		cpu += float64(c)
	}
	if Debug != 0 && cpu > 90 {
		l := len(p.elems)
		fmt.Printf("-----------------------------------------------------------------\npackstat %f%% >90% (%d *stats):\n", cpu, l)
		for i, s := range p.elems {
			c, _ := s.CPU()
			fmt.Printf("packstat >90% (%d/%d): %f %s\n", i, l, c, s.String())
		}
	}
	p.cpu = float32(cpu)
	p.cpuTs = stamp
	return p.cpu, nil
}

func (p *packStat) IO() (uint64, error) {
	if p.ioTs == stamp {
		return p.io, nil
	}
	var io uint64
	for _, s := range p.elems {
		i, _ := s.IO()
		io += i
	}
	p.io = io
	p.ioTs = stamp
	return p.io, nil
}

func (p *packStat) IObps() (uint64, error) {
	if p.iobpsTs == stamp {
		return p.iobps, nil
	}
	var io uint64
	for _, s := range p.elems {
		i, _ := s.IObps()
		io += i
	}
	p.iobps = io
	p.iobpsTs = stamp
	return p.iobps, nil
}

func (p *packStat) GID() (int32, error) {
	if p.gid == -2 {
		return -2, nil // other
	}
	if p.uid == -1 {
		return -1, errNoID
	}
	return p.gid, nil

}

func (p *packStat) UID() (int32, error) {
	if p.uid == -2 {
		return -2, nil // other
	}
	if p.uid == -1 {
		return -1, errNoID
	}
	return p.uid, nil
}

func (p *packStat) Group() (string, error) {
	if p.other != "" {
		return p.other, nil
	}
	if p.gid < 0 {
		return "[unknown]", nil
	}
	return GIDtoName(p.gid), nil
}

func (p *packStat) User() (string, error) {
	if p.other != "" {
		return p.other, nil
	}
	if p.uid < 0 {
		return "[unknown]", nil
	}
	return UIDtoName(p.uid), nil
}

func (p *packStat) Cmd() (string, error) {
	if p.other != "" {
		return p.other, nil
	}
	return p.cmd, nil
}

func (p *packStat) Exe() (string, error) {
	if p.other != "" {
		return p.other, nil
	}
	return "", nil
}

func (p *packStat) CmdLine() (string, error) {
	if p.other != "" {
		return p.other, nil
	}
	return "", nil
}

func (p *packStat) Path() (string, error) {
	if p.other != "" {
		return p.other, nil
	}
	return "", nil
}

func (p *packStat) RSS() (uint64, error) {
	if p.rssTs == stamp {
		return p.rss, nil
	}
	var sum uint64
	for _, s := range p.elems {
		v, _ := s.RSS()
		sum += v
	}
	p.rss = sum
	p.rssTs = stamp
	return sum, nil
}

func (p *packStat) VSZ() (uint64, error) {
	if p.vszTs == stamp {
		return p.vsz, nil
	}
	var sum uint64
	for _, s := range p.elems {
		v, _ := s.VSZ()
		sum += v
	}
	p.vsz = sum
	p.vszTs = stamp
	return sum, nil
}

func (p *packStat) Swap() (uint64, error) {
	if p.swapTs == stamp {
		return p.swap, nil
	}
	var sum uint64
	for _, s := range p.elems {
		v, _ := s.Swap()
		sum += v
	}
	p.swap = sum
	p.swapTs = stamp
	return sum, nil
}

func (p *packStat) ThreadNumber() (uint64, error) {
	if p.threadNbTs == stamp {
		return p.threadNb, nil
	}
	var sum uint64
	for _, s := range p.elems {
		v, _ := s.ThreadNumber()
		sum += v
	}
	p.threadNb = sum
	p.threadNbTs = stamp
	return sum, nil
}

func (p *packStat) FDNumber() (uint64, error) {
	if p.fdNbTs == stamp {
		return p.fdNb, nil
	}
	var sum uint64
	for _, s := range p.elems {
		v, _ := s.FDNumber()
		sum += v
	}
	p.fdNb = sum
	p.fdNbTs = stamp
	return sum, nil
}

func (p *packStat) ChildrenPIDs(depth int) []tPid {
	mall := map[tPid]interface{}{}
	// reccursively get all children as a slice of gopsutils processes
	for _, s := range p.elems {
		pids := s.ChildrenPIDs(depth)
		for _, pid := range pids {
			mall[pid] = nil
		}
	}
	all := make([]tPid, len(mall))
	for pid, _ := range mall {
		all = append(all, pid)
	}
	return all
}

func (p *packStat) Var(name string) string {
	val, known := p.vars[name]
	if known {
		return val
	} else {
		return ""
	}
}

func (p *packStat) PVars() *(map[string]string) {
	return &p.vars
}
