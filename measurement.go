package procfilter

import (
	"regexp"
	"strconv"

	"github.com/influxdata/telegraf"
)

/* A measurement is used to define what tag/values of a set of (filtered/aggregated) processes will be output.
A measurement contains one filter and thus implements the filter interface by proxy.
*/
type measurement struct {
	name    string
	tags    []string
	fields  []string
	noReSub bool // This measurement has no regexp substitution.
	f       filter
}

var shortLivedString string = "[short lived]" // If a process (of pack) is very short lived we sometimes come too late to get some of its fields (cmd, exe, ...). In this case we report this string.

func (m *measurement) Apply() error {
	return m.f.Apply()
}

func (m *measurement) parse(p *Parser) error {
	return p.syntaxError("measurement.Parse() should not be called")
}

func (m *measurement) Stats() *stats {
	return m.f.Stats()
}

func (m *measurement) getTags(s stat, prefix string) (map[string]string, error) {
	tags := map[string]string{}
	var prefTag string
	for _, tag := range m.tags {
		if prefix == "" {
			prefTag = tag
		} else {
			prefTag = prefix + tag
		}
		v, err := tagNameToValue(s, tag)
		if err != nil || v == "" {
			continue
		}
		tags[prefTag] = v
	}
	return tags, nil
}

func tagNameToValue(s stat, name string) (string, error) {
	switch name {
	case "user":
		return s.User()
	case "group":
		return s.Group()
	case "cmd":
		v, err := s.Cmd()
		if err != nil || v == "" {
			v = shortLivedString
		}
		return v, nil
	case "exe":
		return s.Exe()
	case "pid":
		return strconv.Itoa(int(s.PID())), nil
	case "uid":
		v, err := s.UID()
		return strconv.Itoa(int(v)), err
	case "gid":
		v, err := s.GID()
		return strconv.Itoa(int(v)), err
	default:
		return s.Var(name), nil
	}
}

func (m *measurement) getFields(s stat, prefix string) (map[string]interface{}, error) {
	fields := map[string]interface{}{}
	var prefField string
	for _, field := range m.fields {
		if prefix == "" {
			prefField = field
		} else {
			prefField = prefix + field
		}
		switch field {
		case "user":
			v, err := s.User()
			if err != nil {
				continue
			}
			fields[prefField] = v
		case "group":
			v, err := s.Group()
			if err != nil {
				continue
			}
			fields[prefField] = v
		case "cmd":
			v, _ := s.Cmd()
			if v == "" {
				continue
			}
			fields[prefField] = v
		case "exe":
			v, _ := s.Exe()
			if v == "" {
				continue
			}
			fields[prefField] = v
		case "path":
			v, _ := s.Path()
			if v == "" {
				continue
			}
			fields[prefField] = v
		case "cmd_line", "cmdline", "commandline":
			v, _ := s.CmdLine()
			if v == "" {
				continue
			}
			fields[prefField] = v
		case "pid":
			pid := int64(s.PID())
			if pid >= 0 { // Do not output internal  pack"  pseudo PIDs.
				fields[prefField] = pid
			}
		case "uid":
			v, err := s.UID()
			if err != nil {
				continue
			}
			fields[prefField] = int64(v)
		case "gid":
			v, err := s.GID()
			if err != nil {
				continue
			}
			fields[prefField] = int64(v)
		case "rss":
			v, err := s.RSS()
			if err != nil {
				continue
			}
			fields[prefField] = v
		case "vsz":
			v, err := s.VSZ()
			if err != nil {
				continue
			}
			fields[prefField] = v
		case "swap":
			v, err := s.Swap()
			if err != nil {
				continue
			}
			fields[prefField] = v
		case "iops":
			v, err := s.IO()
			if stamp == 1 {
				// CPU is not known until 2nd sample
				continue
			}
			if err != nil {
				continue
			}
			fields[prefField] = v
		case "iobps":
			v, err := s.IObps()
			if stamp == 1 {
				// CPU is not known until 2nd sample
				continue
			}
			if err != nil {
				continue
			}
			fields[prefField] = v
		case "cpu", "cpu_percent":
			v, err := s.CPU()
			if stamp == 1 {
				// CPU is not known until 2nd sample
				continue
			}
			if err != nil {
				continue
			}
			fields[prefField] = v
		case "thread_nb":
			v, err := s.ThreadNumber()
			if err != nil {
				continue
			}
			fields[prefField] = v
		case "fd_nb":
			v, err := s.FDNumber()
			if err != nil {
				continue
			}
			fields[prefField] = v
		case "process_nb":
			v := s.ProcessNumber()
			fields[prefField] = v
		default:
			v := s.Var(field)
			if v != "" {
				fields[prefField] = v
			}
		}
	}
	return fields, nil
}

//var reMeasurementSubstitution = regexp.MustCompile("${[^}]*}|$[a-zA-Z0-9_]*")
var reMeasurementSubstitution = regexp.MustCompile("[$][a-zA-Z0-9_]+")

// Push the current tag/fields of a measuremnt to a telegraf accumulator.
func (m *measurement) push(p *ProcFilter, acc telegraf.Accumulator) {
	// That measurement needs a substitution?
	var mis [][]int
	if !m.noReSub {
		mis = reMeasurementSubstitution.FindAllIndex([]byte(m.name), -1)
	}
	apsMutex.Lock()
	defer apsMutex.Unlock()
	iStats := m.f.Stats()
	for _, ps := range iStats.pid2Stat {
		//pn := ps.ProcessNumber()
		// Hard to process properly the null data points in inflix. So we emit the useless 0 anyway. if pn <= 0 {
		// This stat contains 0 processes => do not output, this way we save network and DB space.
		//	continue
		//}
		tags, err := m.getTags(ps, p.Tag_prefix)
		if err != nil {
			logErr(err.Error())
			continue
		}
		fields, err := m.getFields(ps, p.Field_prefix)
		if err != nil {
			logErr(err.Error())
			continue
		}

		// Substitute part of the measurement name by a tag. Usualy a generated variable. (see revar filter)
		// wl.oracle.${instance} -> wl.oracle.mine
		var nc string
		if mis == nil {
			// Nothing to substitute. (the usual case)
			nc = p.Measurement_prefix + m.name
			// Disable re matching from now on.
			m.noReSub = true
		} else {
			// Need substitution(s)
			nc = p.Measurement_prefix
			ie := 0
			for _, mi := range mis {
				s := m.name[mi[0]:mi[1]] // $name or ${name}
				ss := 1
				se := len(s)
				if s[1] == '{' {
					ss = 2
					se--
				}
				v, err := tagNameToValue(ps, s[ss:se])
				if err == nil {
					nc += m.name[ie:mi[0]] + v
				} else {
					nc += m.name[ie:mi[0]] + "_unknown"
				}
				ie = mi[1]
			}
			// Add the (optional) trailing part.
			nc += m.name[ie:]
		}
		acc.AddFields(nc, fields, tags)
	}
}
