package procfilter

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Reserved variable names (accessible by tag() of field()) that are available builtins and that should not be overriden by a user declared dynamic variable.
var reservedVarNames = map[string]interface{}{
	"cpu":        nil,
	"process_nb": nil,
	"processnb":  nil,
	"thread_nb":  nil,
	"threadnb":   nil,
	"rss":        nil,
	"vsz":        nil,
	"swap":       nil,
	"iops":       nil,
	"iobps":      nil,
	"cmd":        nil,
	"cmdline":    nil,
	"cmd_line":   nil,
	"exe":        nil,
	"path":       nil,
}

/* A filter will select a set of processes.
A filter can be used as input for other filters. Finally a filter can be used by a measurement that will output some tags/fields related to selected processes.
*/
type filter interface {
	Parse(p *Parser) error // Parse the parameters for this filter.
	Stats() *stats         // Get the concrete stats for this filter (what has been selected by the filter)
	Apply() error          // Apply the filter to its inputs (reccursively). Evaluation is lazy and done only once per sample.
}

// name2FuncFilter use a name to return an object of the proper concrete type matchingt the filter interface.
func name2FuncFilter(funcName string) filter {
	// sorry, tried to do that with reflct but did not manage to make it work
	fn := strings.ToLower(funcName)
	var f filter
	switch fn {
	case "all": // both syntax all and all() are allowed
		f = new(allFilter)
	case "top":
		f = new(topFilter)
	case "exceed":
		f = new(exceedFilter)
	case "user":
		f = new(userFilter)
	case "group":
		f = new(groupFilter)
	case "children":
		f = new(childrenFilter)
	case "command", "cmd":
		f = new(cmdFilter)
	case "exe":
		f = new(exeFilter)
	case "path":
		f = new(pathFilter)
	case "cmdline":
		f = new(cmdlineFilter)
	case "pid":
		f = new(pidFilter)
	case "or", "union":
		f = new(orFilter)
	case "and", "intersection":
		f = new(andFilter)
	case "not", "complement":
		f = new(notFilter)
	case "xor", "difference":
		f = new(notFilter)
	case "pack":
		f = new(packFilter)
	case "unpack":
		f = new(unpackFilter)
	case "packby", "by", "pack_by":
		f = new(packByFilter)
	case "filters":
		f = new(filtersFilter)
	case "revar":
		f = new(revarFilter)
	default:
		f = nil
	}
	return f
}

// All processes on the server.
type allFilter struct {
	stats
}

func (f *allFilter) Apply() error {
	// This set of stats is updated once every sample by the Gather() method
	f.pid2Stat = allStats
	return nil
}

func (f *allFilter) Parse(p *Parser) error {
	// eg: all()
	// the all with not () is handled as a named filter that requires no special parsing
	return p.parseSymbol(')')
}

func (f *allFilter) Stats() *stats {
	return &f.stats
}

// Set a variable using  the rewriten form of some other string.
type revarFilter struct {
	stats
	crit    string // field to rewrite criteria (eg: cmd, cmdline)
	match   string
	re      *regexp.Regexp // match RE (once compiled.)
	rewrite string         // replace string (can contain groups eg: 'foo$1bar$2')
	vn      string         // variable name for the rewritten string.
	inputs  []filter
}

func (f *revarFilter) Apply() error {
	err := applyAll(f.inputs)
	if err != nil {
		return err
	}
	// A rewrite filter will not filter out any process (it may only rewrite some of its fields)
	if len(f.inputs) == 1 {
		// simple case with only one input -> copy its stats
		f.pid2Stat = f.inputs[0].Stats().pid2Stat
	} else {
		// Build a new map from all procstats found in inputs.
		for _, input := range f.inputs {
			for pid, s := range input.Stats().pid2Stat {
				f.pid2Stat[pid] = s
			}
		}
	}
	p2s := f.pid2Stat

	for _, s := range p2s {
		pvars := s.PVars()
		vars := *pvars
		if _, known := vars[f.vn]; !known { // This variable has never been set.
			var orig string
			switch f.crit {
			case "cmd":
				orig, _ = s.Cmd()
			case "exe":
				orig, _ = s.Exe()
			case "cmdline", "cmd_line":
				orig, _ = s.CmdLine()
			case "user":
				orig, _ = s.User()
			case "group":
				orig, _ = s.Group()
			default: // Assume the criteria is in fact a variable name.
				orig = s.Var(f.crit)
			}

			if f.re.MatchString(orig) {
				rew := f.re.ReplaceAllString(orig, f.rewrite)
				rew = strings.ToLower(rew)
				if *pvars == nil {
					vars = map[string]string{}
					*pvars = vars // *pvars BUG
				}
				vars[f.vn] = rew
			}
		}
	}
	return nil
}

func (f *revarFilter) Parse(p *Parser) error {
	// eg: rewrite(cmd,"^jbd2|flush|kswapd$","kernel",recmd)
	err := p.parseArgIdentifier(&f.crit)
	if err != nil {
		return err
	}
	err = p.parseArgString(&f.match)
	if err != nil {
		return err
	}
	re, err := regexp.Compile(f.match)
	if err != nil {
		return err
	}
	f.re = re
	err = p.parseArgString(&f.rewrite)
	if err != nil {
		return err
	}
	err = p.parseArgIdentifier(&f.vn)
	if err != nil {
		return err
	}
	if _, reserved := reservedVarNames[f.vn]; reserved {
		return p.syntaxError(fmt.Sprintf("declaring a variable with a reserved name '%s'", f.vn))
	}
	err = p.parseArgFilterList(&f.inputs, 1)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *revarFilter) Stats() *stats {
	return &f.stats
}

// Select the most (top) consuming given a criteria.
type topFilter struct {
	stats
	topNb int64  // how many process to keep
	crit  string // sort criteria
	input filter
}

func (f *topFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	input := f.input
	err := input.Apply()
	if err != nil {
		return err
	}
	iStats := input.Stats()
	stats := []stat{}
	// Convert the stat map to a slice without 0 as criteria.
	// (removing the 0 speed up the sort and stabilizes the top result when there are only 0 criteria)
	// TODO use funct(n pointer?
	switch f.crit {
	case "rss":
		for _, s := range iStats.pid2Stat {
			v, err := s.RSS()
			if err != nil {
				continue
			}
			if v > 0 {
				stats = append(stats, s)
			}
		}
	case "vsz":
		for _, s := range iStats.pid2Stat {
			v, err := s.VSZ()
			if err != nil {
				continue
			}
			if v > 0 {
				stats = append(stats, s)
			}
		}
	case "swap":
		for _, s := range iStats.pid2Stat {
			v, err := s.Swap()
			if err != nil {
				continue
			}
			if v > 0 {
				stats = append(stats, s)
			}
		}
	case "thread_nb":
		for _, s := range iStats.pid2Stat {
			v, err := s.ThreadNumber()
			if err != nil {
				continue
			}
			if v > 0 {
				stats = append(stats, s)
			}
		}
	case "fd_nb":
		for _, s := range iStats.pid2Stat {
			v, err := s.FDNumber()
			if err != nil {
				continue
			}
			if v > 0 {
				stats = append(stats, s)
			}
		}
	case "process_nb":
		for _, s := range iStats.pid2Stat {
			v := s.ProcessNumber()
			if v > 0 {
				stats = append(stats, s)
			}
		}
	case "cpu":
		for _, s := range iStats.pid2Stat {
			v, err := s.CPU()
			if err != nil {
				continue
			}
			if v > 0 {
				stats = append(stats, s)
			}
		}
	case "io":
		for _, s := range iStats.pid2Stat {
			v, err := s.IO()
			if err != nil {
				continue
			}
			if v > 0 {
				stats = append(stats, s)
			}
		}
	case "iobps":
		for _, s := range iStats.pid2Stat {
			v, err := s.IObps()
			if err != nil {
				continue
			}
			if v > 0 {
				stats = append(stats, s)
			}
		}
	default:
		return fmt.Errorf("unknownsort criteria %q", f.crit)
	}
	// sort it according to rss
	switch f.crit {
	case "rss":
		sort.Sort(byRSS(stats))
	case "vsz":
		sort.Sort(byVSZ(stats))
	case "swap":
		sort.Sort(bySwap(stats))
	case "thread_nb":
		sort.Sort(byThreadNumber(stats))
	case "fd_nb":
		sort.Sort(byFDNumber(stats))
	case "process_nb":
		sort.Sort(byProcessNumber(stats))
	case "cpu":
		sort.Sort(byCPU(stats))
	case "io":
		sort.Sort(byIO(stats))
	case "iobps":
		sort.Sort(byIObps(stats))
	default:
		return fmt.Errorf("unknownsort criteria %q", f.crit)
	}
	// build this filter procstat map (a subset of the one in input filter)
	l := min(int(f.topNb), len(stats))
	m := make(map[tPid]stat, l+1) // keep room for the "other"  stat
	for i := tPid(0); i < tPid(l); i++ {
		s := stats[i]
		m[s.PID()] = s
	}
	// Add an "other" packStat with all procStat not in top.
	ss := unpackSliceAsSlice(stats[l:], nil)
	o := NewPackStat(ss)
	o.other = fmt.Sprintf("_other.top.%s.%d", f.crit, f.topNb)
	m[o.PID()] = o
	f.pid2Stat = m
	return nil
}

func (f *topFilter) Parse(p *Parser) error {
	// eg: [top(]rss,5,filter
	err := p.parseArgIdentifier(&f.crit)
	if err != nil {
		return err
	}
	err = p.parseArgInt(&f.topNb)
	if err != nil {
		return err
	}
	err = p.parseArgLastFilter(&f.input)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *topFilter) Stats() *stats {
	return &f.stats
}

// Select what exceeds a criteria/value limit.
type exceedFilter struct {
	stats
	rv    string // unparsed value
	iv    int64
	fv    float64
	crit  string
	input filter
}

func (f *exceedFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	input := f.input
	err := input.Apply()
	if err != nil {
		return err
	}
	iStats := input.Stats()
	eo := []*procStat{}
	m := map[tPid]stat{}
	for pid, s := range iStats.pid2Stat {
		switch f.crit { // TODO invert for<->switch for perfformance?
		case "rss":
			rss, err := s.RSS()
			if err != nil {
				continue
			}
			if int64(rss) > f.iv {
				m[pid] = s
			} else {
				eo = unpackStatAsSlice(s, eo)
			}
		case "vsz":
			vsz, err := s.VSZ()
			if err != nil {
				continue
			}
			if int64(vsz) > f.iv {
				m[pid] = s
			} else {
				eo = unpackStatAsSlice(s, eo)
			}
		case "swap":
			swap, err := s.Swap()
			if err != nil {
				continue
			}
			if int64(swap) > f.iv {
				m[pid] = s
			} else {
				eo = unpackStatAsSlice(s, eo)
			}
		case "thread_nb":
			tnb, err := s.ThreadNumber()
			if err != nil {
				continue
			}
			if int64(tnb) > f.iv {
				m[pid] = s
			} else {
				eo = unpackStatAsSlice(s, eo)
			}
		case "fd_nb":
			tnb, err := s.FDNumber()
			if err != nil {
				continue
			}
			if int64(tnb) > f.iv {
				m[pid] = s
			} else {
				eo = unpackStatAsSlice(s, eo)
			}
		case "process_nb":
			pnb := s.ProcessNumber()
			if int64(pnb) > f.iv {
				m[pid] = s
			} else {
				eo = unpackStatAsSlice(s, eo)
			}
		case "cpu":
			cpu, err := s.CPU()
			if err != nil {
				continue
			}
			if float64(cpu) > f.fv {
				m[pid] = s
			} else {
				eo = unpackStatAsSlice(s, eo)
			}
		case "io":
			io, err := s.IO()
			if err != nil {
				continue
			}
			if int64(io) > f.iv {
				m[pid] = s
			} else {
				eo = unpackStatAsSlice(s, eo)
			}
		case "iobps":
			io, err := s.IObps()
			if err != nil {
				continue
			}
			if int64(io) > f.iv {
				m[pid] = s
			} else {
				eo = unpackStatAsSlice(s, eo)
			}
		default:
			return fmt.Errorf("unknown sort criteria %q", f.crit)
		}
	}
	// Pack all other procStat in one stat.
	o := NewPackStat(eo)
	o.other = fmt.Sprintf("_other.exceed.%s.%s", f.crit, f.rv)
	m[o.PID()] = o
	f.pid2Stat = m
	return nil
}

func (f *exceedFilter) Parse(p *Parser) error {
	// eg: [exceed(]rss,1G)
	// TODO eg: [exceed(]rss,10%)
	// TODO eg: [exceed(]cpu,20%)
	var err error
	err = p.parseArgIdentifier(&f.crit)
	if err != nil {
		return err
	}
	// Keep a copy of the human provided value for later string output.
	_, lit := p.scanIgnoreWhitespace()
	p.unscan()
	f.rv = lit
	// Parse the value depending on the chosen criteria.
	switch f.crit {
	case "rss", "vsz", "swap", "thread_nb", "process_nb", "fd_nb":
		err := p.parseArgInt(&f.iv)
		if err != nil {
			return p.syntaxError(fmt.Sprintf("exceed with '%s; criteri requires an integer as threshold", f.crit))
		}
	case "cpu":
		var v int64
		err := p.parseArgInt(&v)
		if err != nil {
			return p.syntaxError(fmt.Sprintf("exceed with '%s; criteri requires an integer as threshold", f.crit))
		}
		f.fv = float64(v)
	default:
		return fmt.Errorf("unknownexceed criteria %q", f.crit)
	}

	err = p.parseArgLastFilter(&f.input)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *exceedFilter) Stats() *stats {
	return &f.stats
}

// Select matching PIDs.
type pidFilter struct {
	stats
	pid   tPid
	file  string
	input filter
}

func (f *pidFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	input := f.input
	err := input.Apply()
	if err != nil {
		return err
	}
	sm := map[tPid]stat{}
	f.pid2Stat = sm
	iStats := input.Stats()
	if len(iStats.pid2Stat) == 0 {
		return nil
	}
	if f.file != "" {
		// get the PID from a file
		pid, err := pidFromFile(f.file)
		if err != nil {
			return err
		}
		f.pid = pid
	}
	for pid, s := range iStats.pid2Stat {
		ipid := s.PID()
		if ipid == f.pid {
			sm[pid] = s
		}
	}
	return nil
}

func (f *pidFilter) Parse(p *Parser) error {
	// eg: [user(]"joe",all)
	tok, lit := p.scanIgnoreWhitespace()
	if tok == tTString {
		f.file = lit
	} else if tok == tTNumber {
		{
		}
		i, err := strconv.Atoi(lit)
		if err != nil {
			return p.syntaxError(fmt.Sprintf("found %q, expecting an integer", lit))
		}
		f.pid = tPid(i)
	} else {
		return p.syntaxError(fmt.Sprintf("found %q, expecting a string or number", lit))
	}
	err := p.parseArgLastFilter(&f.input)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *pidFilter) Stats() *stats {
	return &f.stats
}

// Select matching user.
type userFilter struct {
	stats
	name   *stregexp
	id     int32
	inputs []filter
}

func (f *userFilter) Apply() error {
	if !f.stats.reset() || f.id == -2 {
		return nil
	}
	err := applyAll(f.inputs)
	if err != nil {
		return err
	}
	pss := unpackFiltersAsSlice(f.inputs, nil)
	sm := map[tPid]stat{}
	if f.id != -1 { // Filter on numeric UID of a string that has been converted to an UID.
		for _, ps := range pss {
			id, err := ps.UID()
			if err != nil {
				continue
			}
			if id == f.id {
				sm[ps.pid] = stat(ps)
			}
		}
	} else { // Filter on name.
		for _, ps := range pss {
			name, err := ps.User()
			if err != nil {
				continue
			}
			if f.name.matchString(name) {
				sm[ps.pid] = stat(ps)
			}
		}
	}
	f.stats.pid2Stat = sm
	return nil
}

func (f *userFilter) Parse(p *Parser) error {
	// eg: [user(]"foo",all)
	tok, lit := p.scanIgnoreWhitespace()
	p.unscan()
	switch tok {
	case tTString:
		f.id = -2
		var name string
		err := p.parseArgString(&name)
		if err != nil {
			return p.syntaxError(err.Error())
		}
		id, err := NametoUID(name)
		if err != nil {
			// Consider that an unkown user is not a real error but emit a wanring and disable the filter. (but continue parsing the script)
			logWarning(err.Error())
		} else {
			// clear the -2 value that stands for 'disable'
			f.id = id
		}
	case tTRegexp:
		f.id = -1 // -1 means use the string match not this id.
		err := p.parseArgStregexp(&f.name)
		if err != nil {
			return p.syntaxError(err.Error())
		}
	case tTNumber:
		var i int64
		err := p.parseArgInt(&i)
		if err != nil {
			return err
		}
		f.id = int32(i)
	default:
		return p.syntaxError(fmt.Sprintf("found %q, expecting a string or number", lit))
	}
	err := p.parseArgFilterList(&f.inputs, 0)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *userFilter) Stats() *stats {
	return &f.stats
}

// Select matching group.
type groupFilter struct {
	stats
	name   *stregexp
	id     int32
	inputs []filter
}

// TODO OPTIM use the same name->GID optimization as for userFilter.
func (f *groupFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	err := applyAll(f.inputs)
	if err != nil {
		return err
	}
	pss := unpackFiltersAsSlice(f.inputs, nil)
	sm := map[tPid]stat{}
	if f.name == nil { // Filter on numeric ID.
		for _, ps := range pss {
			id, err := ps.GID()
			if err != nil {
				continue
			}
			if id == f.id {
				sm[ps.pid] = stat(ps)
			}
		}
	} else { // Filter on name.
		for _, ps := range pss {
			name, err := ps.Group()
			if err != nil {
				continue
			}
			if f.name.matchString(name) {
				sm[ps.pid] = stat(ps)
			}
		}
	}
	f.stats.pid2Stat = sm
	return nil
}

func (f *groupFilter) Parse(p *Parser) error {
	// eg: [group(]"foo",all)
	tok, lit := p.scanIgnoreWhitespace()
	p.unscan()
	switch tok {
	case tTString, tTRegexp:
		err := p.parseArgStregexp(&f.name)
		if err != nil {
			return p.syntaxError(err.Error())
		}
	case tTNumber:
		var i int64
		err := p.parseArgInt(&i)
		if err != nil {
			return err
		}
		f.id = int32(i)
	default:
		return p.syntaxError(fmt.Sprintf("found %q, ing a string or number", lit))
	}
	err := p.parseArgFilterList(&f.inputs, 0)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *groupFilter) Stats() *stats {
	return &f.stats
}

// Select children.
type childrenFilter struct {
	stats
	depth  int64
	inputs []filter
}

func (f *childrenFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	err := applyAll(f.inputs)
	if err != nil {
		return err
	}
	pss := unpackFiltersAsSlice(f.inputs, nil)
	if pss == nil {
		return nil
	}
	tree := map[tPid]*procStat{}
	getChildren(int(f.depth), pss, tree)
	for pid, ps := range tree {
		f.pid2Stat[pid] = stat(ps)
	}
	return nil
}

func (f *childrenFilter) Parse(p *Parser) error {
	// eg: [childre(]f1,f2,5)
	err := p.parseArgFilterList(&f.inputs, 0)
	if err != nil {
		return err
	}
	// optional depth
	err = p.parseArgInt(&f.depth)
	if err == nil {
		if f.depth <= 0 {
			return p.syntaxError(fmt.Sprintf("depth in children filter must be >= 1, found '%d'", f.depth))
		}
	}
	if f.depth == 0 {
		// A depth of 0 means we want to get all descendants
		// But we fix a limit to avoid any fishy cycle in the gopsutil code
		f.depth = 1024
	}
	return p.parseSymbol(')')
}

func (f *childrenFilter) Stats() *stats {
	return &f.stats
}

/* Filters related to the command line (exe, cmdline)
 */

// Select matching command name (basename).
type cmdFilter struct {
	stats
	pat    *stregexp
	inputs []filter
}

func (f *cmdFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	err := applyAll(f.inputs)
	if err != nil {
		return err
	}
	for _, input := range f.inputs {
		iStats := input.Stats()
		for pid, s := range iStats.pid2Stat {
			pat, _ := s.Cmd()
			if err != nil {
				continue
			}
			if f.pat.matchString(pat) {
				f.pid2Stat[pid] = s
			}
		}
	}
	return nil
}

func (f *cmdFilter) Parse(p *Parser) error {
	// eg: name("apa.*",all)
	err := p.parseArgStregexp(&f.pat)
	if err != nil {
		return err
	}
	err = p.parseArgFilterList(&f.inputs, 0)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *cmdFilter) Stats() *stats {
	return &f.stats
}

// Select matching exe name (full path to command with dirname and basename).
type exeFilter struct {
	stats
	pat    *stregexp
	inputs []filter
}

func (f *exeFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	err := applyAll(f.inputs)
	if err != nil {
		return err
	}
	for _, input := range f.inputs {
		iStats := input.Stats()
		for pid, s := range iStats.pid2Stat {
			pat, _ := s.Exe()
			if err != nil {
				continue
			}
			if f.pat.matchString(pat) {
				f.pid2Stat[pid] = s
			}
		}
	}
	return nil
}

func (f *exeFilter) Parse(p *Parser) error {
	// eg: name("apa.*",all)
	err := p.parseArgStregexp(&f.pat)
	if err != nil {
		return err
	}
	err = p.parseArgFilterList(&f.inputs, 0)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *exeFilter) Stats() *stats {
	return &f.stats
}

// Select matching path (dirname of the command).
type pathFilter struct {
	stats
	pat    *stregexp
	inputs []filter
}

func (f *pathFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	err := applyAll(f.inputs)
	if err != nil {
		return err
	}
	for _, input := range f.inputs {
		iStats := input.Stats()
		for pid, s := range iStats.pid2Stat {
			pat, _ := s.Path()
			if err != nil {
				continue
			}
			if f.pat.matchString(pat) {
				f.pid2Stat[pid] = s
			}
		}
	}
	return nil
}

func (f *pathFilter) Parse(p *Parser) error {
	// eg: name("apa.*",all)
	err := p.parseArgStregexp(&f.pat)
	if err != nil {
		return err
	}
	err = p.parseArgFilterList(&f.inputs, 0)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *pathFilter) Stats() *stats {
	return &f.stats
}

// Select matching command line (the full command as one big string)
type cmdlineFilter struct {
	stats
	pat    *stregexp
	inputs []filter
}

func (f *cmdlineFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	err := applyAll(f.inputs)
	if err != nil {
		return err
	}
	for _, input := range f.inputs {
		iStats := input.Stats()
		for pid, s := range iStats.pid2Stat {
			pat, _ := s.CmdLine()
			if err != nil {
				continue
			}
			if f.pat.matchString(pat) {
				f.pid2Stat[pid] = s
			}
		}
	}
	return nil
}

func (f *cmdlineFilter) Parse(p *Parser) error {
	// eg: name("apa.*",all)
	err := p.parseArgStregexp(&f.pat)
	if err != nil {
		return err
	}
	err = p.parseArgFilterList(&f.inputs, 0)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *cmdlineFilter) Stats() *stats {
	return &f.stats
}

/* Filters based on set algebra.
 */

// Select and unpack the union of input filters.
type orFilter struct {
	stats
	inputs []filter
}

func (f *orFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	err := applyAll(f.inputs)
	if err != nil {
		return err
	}
	// TODO optim if only one input?
	for _, input := range f.inputs {
		pss := input.Stats().unpackAsSlice(nil)
		for _, ps := range pss {
			f.pid2Stat[ps.pid] = ps
		}
	}
	return nil
}

func (f *orFilter) Parse(p *Parser) error {
	// eg: [or(]f1,f2,f3)
	err := p.parseArgFilterList(&f.inputs, 2)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *orFilter) Stats() *stats {
	return &f.stats
}

// Select and unpack the inersection of input filters.
type andFilter struct {
	stats
	inputs []filter
}

func (f *andFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	err := applyAll(f.inputs)
	if err != nil {
		return err
	}
	counts := map[*procStat]int{}
	for _, input := range f.inputs {
		pss := input.Stats().unpackAsSlice(nil)
		for _, ps := range pss {
			counts[ps]++
		}
	}
	intersect := map[tPid]stat{}
	li := len(f.inputs)
	for ps, count := range counts {
		if count == li {
			// This PID appears once per input filter.
			intersect[ps.pid] = stat(ps)
		}
	}
	f.pid2Stat = intersect
	return nil
}

func (f *andFilter) Parse(p *Parser) error {
	// eg: [and(]f1,f2,f3)
	err := p.parseArgFilterList(&f.inputs, 2)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *andFilter) Stats() *stats {
	return &f.stats
}

// Select and unpack the complement of input filters.
type notFilter struct {
	stats
	inputs []filter
}

func (f *notFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	err := applyAll(f.inputs)
	if err != nil {
		return err
	}
	psm := unpackFiltersAsMap(f.inputs, nil)
	complement := map[tPid]stat{}
	// All pids that are in allProcStats and not in input filter
	for pid, ps := range allStats {
		if _, in := psm[pid]; !in {
			complement[pid] = ps
		}
	}
	f.pid2Stat = complement
	return nil
}

func (f *notFilter) Parse(p *Parser) error {
	// eg: [not(]f1,f2,f3)
	err := p.parseArgFilterList(&f.inputs, 1)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *notFilter) Stats() *stats {
	return &f.stats
}

// Select and unpack the synthetic difference of input filters. (what is in exactly one input filter)
type differenceFilter struct {
	stats
	inputs []filter
}

func (f *differenceFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	err := applyAll(f.inputs)
	if err != nil {
		return err
	}
	counts := map[*procStat]int{}
	for _, input := range f.inputs {
		pss := input.Stats().unpackAsSlice(nil)
		for _, ps := range pss {
			counts[ps]++
		}
	}
	difference := map[tPid]stat{}
	for ps, count := range counts {
		if count == 1 {
			// This PID appears only once so it belongs ony to one filter.
			difference[ps.pid] = stat(ps)
		}
	}
	f.pid2Stat = difference
	return nil
}

func (f *differenceFilter) Parse(p *Parser) error {
	// eg: [difference(]f1,f2,f3)
	err := p.parseArgFilterList(&f.inputs, 2)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *differenceFilter) Stats() *stats {
	return &f.stats
}

// Aggregate/gather all input filter as one sythetic workload
type packFilter struct {
	stats  //
	inputs []filter
}

func (f *packFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	err := applyAll(f.inputs)
	if err != nil {
		return err
	}
	// TODO optimize if the only input is already a pack -> use it instead of recreating one.
	// Pack/Gather all input procStat.
	pss := unpackFiltersAsSlice(f.inputs, nil)
	s := NewPackStat(pss)
	// a pakcStat contains only one ID (but packed stats are in .elems)
	f.pid2Stat = map[tPid]stat{}
	f.pid2Stat[s.PID()] = s
	return nil
}

func (f *packFilter) Parse(p *Parser) error {
	// eg: [pack(]f1,f2,f3)
	err := p.parseArgFilterList(&f.inputs, 1)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *packFilter) Stats() *stats {
	return &f.stats
}

// Unpack the content of input filters. (get only real procStats by unpacking the packStat)
type unpackFilter struct {
	stats
	inputs []filter
}

func (f *unpackFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	for _, input := range f.inputs {
		iStats := input.Stats()
		for id, s := range iStats.pid2Stat {
			if id > 0 {
				f.pid2Stat[id] = s
			} else {
				// This is a packed stat, need to unpack
				ps := s.(*packStat)
				for _, ss := range ps.elems {
					pid := ss.PID()
					f.pid2Stat[pid] = s
				}
			}
		}
	}
	return nil
}

func (f *unpackFilter) Parse(p *Parser) error {
	// eg: [unpack(]f1,f2,f3)
	err := p.parseArgFilterList(&f.inputs, 1)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *unpackFilter) Stats() *stats {
	return &f.stats
}

// Pack processes using a group criteria. eg packby(user) will create one packStat per user that will contain all processes for this user.
type packByFilter struct {
	stats
	inputs []filter
	by     []string // all criteria to pack by
}

func (f *packByFilter) String() string {
	s := fmt.Sprintf("packby: %s", f.by)
	return s
}

func (f *packByFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	applyAll(f.inputs)
	pss := unpackFiltersAsSlice(f.inputs, nil)
	p := NewPackStat(pss)
	split := []*packStat{p}
	for _, by := range f.by {
		subsplit := []*packStat{}
		for _, p := range split {
			ss := p.packBy(by)
			for _, sp := range ss {
				subsplit = append(subsplit, sp)
			}
		}
		split = subsplit
	}
	// We now have an array of packstats (one per unique tuple of by criteria values)
	for _, p := range split {
		f.pid2Stat[p.pid] = stat(p)
	}
	return nil
}

func (f *packByFilter) Parse(p *Parser) error {
	// eg: [packby(]user,f1,f2,f3)
	// eg: [packby(](user,cmd),f1,f2,f3)
	err := p.parseSymbol('(')
	if err == nil {
		// multi criteria packby (eg: (user, cmd)
		// the ) is consummed
		f.by, err = p.parseIdentifierList()
		if err == nil {
			err = p.parseSymbol(',')
		}
	} else {
		// single criteria packby (eg: user)
		by := make([]string, 1)
		err = p.parseArgIdentifier(&by[0])
		if err == nil {
			f.by = by
		}
	}
	if err != nil {
		return err
	}
	// Then parse the list of filters
	err = p.parseArgFilterList(&f.inputs, 0)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *packByFilter) Stats() *stats {
	return &f.stats
}

// Select filters by their name.
type filtersFilter struct {
	stats
	pat    *stregexp
	inputs []filter
}

func (f *filtersFilter) Apply() error {
	if !f.stats.reset() {
		return nil
	}
	inputs := []filter{}
	for name, filter := range currentParser.n2f {
		if f.pat.matchString(name) {
			inputs = append(inputs, filter)
		}
	}
	f.inputs = inputs
	/* TODO remove
	err := applyAll(f.inputs)
	if err != nil {
		return err
	}*/
	// Collect all stats in all inputs (no unpack but filter out the special "other" packStats
	for _, input := range f.inputs {
		iStats := input.Stats()
		for pid, s := range iStats.pid2Stat {
			if pid < 0 && s.(*packStat).other != "" {
				continue
			}
			f.pid2Stat[pid] = s
		}
	}
	return nil
}

func (f *filtersFilter) Parse(p *Parser) error {
	err := p.parseArgStregexp(&f.pat)
	if err != nil {
		return err
	}
	return p.parseSymbol(')')
}

func (f *filtersFilter) Stats() *stats {
	return &f.stats
}
