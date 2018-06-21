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
	if packStatId <= -2147483647 { // min int32 +1
		packStatId = 0 // reuse the range. Assuming no old pacstat are still alive.
	}
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
	pid   tPid // A fake uniq (negative) PID.
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

	// Quick fix until the >100% bug is found.
	if cpu > 100 {
		if Debug != 0 {
			l := len(p.elems)
			fmt.Printf("----------------------------------------------\n")
			fmt.Printf("--packstat %f > 100 (%d *stats):\n", cpu, l)
			for i, s := range p.elems {
				c, _ := s.CPU()
				fmt.Printf("-- packstat > 100 (%d/%d): %f %s\n", i, l, c, s.String())
			}
		}
		cpu = 100
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
		// If a name is not defined on a packstat we can try to find it in one of its sub stats.
		// This is a cheap and a bit cheaty way to get kind of a aggregation of sub-vars.
		// The right way would be to check if all substats have the same value and return it only in this case.
		// But if the user is trying to get to a sub value. He probably knows that this value is relevant (ie: the same for all stats).
		// So return the first non "" var from substats.
		for _, s := range p.elems {
			v := s.Var(name)
			if v != "" {
				return v
			}
		}
		return p.elems[0].Var(name)
	}
}

func (p *packStat) PVars() *(map[string]string) {
	return &p.vars
}

// packStat Copy all p values to 'to' (all values used as criteria for pakBy and similar).
func (p *packStat) copyByValues(to *packStat) {
	to.uid = p.uid
	to.gid = p.gid
	to.cmd = p.cmd
	if p.vars != nil {
		vars := map[string]string{}
		for k, v := range p.vars {
			vars[k] = v
		}
		to.vars = vars
	}
}

// packby will split stats in input packstat according to a criteria.
func (p *packStat) packBy(by string) []*packStat {
	split := []*packStat{}
	pss := p.elems
	// We will discover the set of values for the by criteria and pack accordingly.
	switch by {
	case "user":
		mby := map[int32]*packStat{}
		for _, ps := range pss {
			v, err := ps.UID()
			if err != nil {
				continue
			}
			if packStat, known := mby[v]; known {
				// Already have a packStat for this user. Append to it.
				packStat.elems = append(packStat.elems, ps)
			} else {
				// New value, create a new packStat for all procStats with that value.
				packStat = NewPackStat([]*procStat{ps})
				p.copyByValues(packStat)
				split = append(split, packStat)
				packStat.uid = v
				mby[v] = packStat
			}
		}
	case "group":
		mby := map[int32]*packStat{}
		for _, ps := range pss {
			v, err := ps.GID()
			if err != nil {
				continue
			}
			if packStat, known := mby[v]; known {
				// Already have a packStat for this user. Append to it.
				packStat.elems = append(packStat.elems, ps)
			} else {
				// New value, create a new packStat for all procStats with that value.
				packStat = NewPackStat([]*procStat{ps})
				p.copyByValues(packStat)
				split = append(split, packStat)
				packStat.gid = v
				mby[v] = packStat
			}
		}
	case "cmd":
		mby := map[string]*packStat{}
		for _, ps := range pss {
			v := ps.cmd
			if packStat, known := mby[v]; known {
				// Already have a packStat for this user. Append to it.
				packStat.elems = append(packStat.elems, ps)
			} else {
				// New value, create a new packStat for all procStats with that value.
				packStat = NewPackStat([]*procStat{ps})
				p.copyByValues(packStat)
				split = append(split, packStat)
				packStat.cmd = v
				mby[v] = packStat
			}
		}
	default: // This is probably a variable name used to store synthetic data.
		mby := map[string]*packStat{}
		for _, ps := range pss {
			v, exist := ps.vars[by]
			if !exist || v == "" {
				v = "(NA)" // Process where the variable does not exist or is empty are packed together with a value of '(NA)'
			}
			if packStat, known := mby[v]; known {
				// Already have a packStat for this user. Append to it.
				packStat.elems = append(packStat.elems, ps)
			} else {
				// New value, create a new packStat for all procStats with that value.
				packStat = NewPackStat([]*procStat{ps})
				p.copyByValues(packStat)
				split = append(split, packStat)
				if packStat.vars == nil {
					packStat.vars = map[string]string{}
				}
				packStat.vars[by] = v
				mby[v] = packStat
			}
		}
	}
	return split
}
