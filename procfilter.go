/*
procfilter is an input plugin designed to improve upon procstat plugin.
- Rather than sending massive amount of data to the DB we offer filters to select the processes using various methods. (top, exceed, children, ...)
- Metrics corresponding to a set of filtered processes can be aggregated to create workloads.
- If possible we also use Netlink kernel event api to detect new processes as soon as possible and hopefuly get CPU counters before they vanish.
- The metric collection mechanism is trying to get pertinent data for short lived processes. A non linear sampling scheme is used (depending on the age of the process) thus we are not constrained by the telegraf gather loop rate.
*/
package procfilter

import (
	"fmt"
	"strings"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs"
)

var curProcFilter *ProcFilter // TODO: refactor. we this ugly global we probably loose the ability to get more thatn one procfilter in the same telegraf process.
var Debug int64 = 0

type ProcFilter struct {
	Script             string  // The script containing commands to process.
	Script_file        string  // If not nit, the file name of a script to load.
	Measurement_prefix string  // String prefix added to all measurement names.
	Tag_prefix         string  // String prefix added to all tag names.
	Field_prefix       string  // String prefix added to all field names.
	Netlink            bool    // Try to use Netlink to get more accurate metrics on short-lived processes?
	Wakeup_interval    int64   // in ms. How often do we wake up to update some stats (only for some young processes, not all processes)
	Update_age_ratio   float64 // last_update/age ratio to trigger a new update.
	Debug              int64   // Debug mask.
	parser             *Parser
	parseOK            bool    // Script parsed OK?
	netlinkOk          bool    // Using netlink?
	needOneScan        bool    // If the netlink gets a transiant error, we ask for a rescan of the /proc dir.
	prevSampleStart    uint64  // Time in unix nanos
	sampleStart        uint64  // Time in unix nanos
	sampleDuration     uint64  // Time in ns. between last and current sample (This is the precise observed duration and not the theoretical 'interval' requested in the telegraf configuration.)
	sampleDurationS    float32 // Time in s. between last and current sample (This is the precise observed duration and not the theoretical 'interval' requested in the telegraf configuration.)
}

func NewProcFilter() *ProcFilter {
	p := &ProcFilter{Measurement_prefix: "pf.", Netlink: true, Wakeup_interval: 100, Update_age_ratio: 0.5, Debug: 0}
	curProcFilter = p
	return p
}

var sampleConfig = `
  ## Full documentation: https://github.com/neoliv/telegraf/blob/master/plugins/inputs/procfilter/README.md
  ## Set various prefixes used in identifiers.
  # measurement_prefix = "pf."
  # tag_prefix = ""
  # field_prefix = ""
  ## Try to use Linux kernel netlink to improve CPU metrics on short-lived processes.
  # netlink = true
  ## Wake up interval for the extra sampling goroutine. Shorter means you will get more accurate metrics (mainly CPU usage) for short lived processes, but it will cost you more CPU.
  ## Note that the ´interval´ telegraf configuration value (eg: 10s) is also used to gather and output metrics. The procfilter wakeup_interval is used to collect extra sample and is useful only for short-lived processes.
  # wakeup_interval = 100 # in ms
  ## Update age ratio indicates to the sampling goroutine when to update metrics for a process depending on its age. Young processes data get extra updates to collect relevant metrics before they vanish.
  # update_age_ratio = 0.5 # 0 => update all processes every time we wakeup (not recommended), 1.0 => update if the last update done is older than the age of the process. 
  ## Debug flag (among other things, will output the script with line numbers).
  # debug = 0 
  ## Describe what you want to measure by writting a script.
  ## (in an external file or embedded here.)
  # script_file = ""
  # script = """
  #   joe_filter <- user('joe')
  #   joe = tags(cmd) fields(rss,cpu) <- top(rss,5,joe_filter)
  #   wl_web = fields(cpu,rss,swap,process_nb) <- pack(user('apache'),group('tomcat',children(cmd('nginx')))
  #   top = tags(exe,user) fields(cpu,rss,pid) <- top(cpu,10)
  #   heavies = tags(user) <- top(rss,5,packby(user))
  # """
  ## Syntax is described in the README.md on github.
`

func (_ *ProcFilter) SampleConfig() string {
	return sampleConfig
}

func (_ *ProcFilter) Description() string {
	return "Monitor process cpu and memory usage with filters and aggregation"
}

func init() {
	inputs.Add("procfilter", func() telegraf.Input {
		return NewProcFilter()
	})
}

func (p *ProcFilter) init() {
	so := "value of script= in configuration file"
	if p.Script_file != "" {
		if p.Script != "" {
			logErr(fmt.Sprintf("E! You cannot have non empty script and script_file at the same time"))
			return
		}
		s, err := fileContent(p.Script_file)
		if err != nil {
			logErr(err.Error())
			return
		}
		so = fmt.Sprintf("content of file '%s'", p.Script_file)
		p.Script = s
	}
	// Init and parse the script to build the AST.
	p.Script = preprocess(p.Script)
	if p.Debug != 0 {
		logWarning(fmt.Sprintf("Debug mode %d. May use more resources.", p.Debug))
		Debug = p.Debug // Need a global to read it in obscure corners of the code.
		logWarning(debugScript(p.Script, -1, -1))
	}
	p.parser = NewParser(strings.NewReader(p.Script))
	err := p.parser.Parse()
	if err != nil {
		logErr(fmt.Sprintf("%s\n%s", err.Error(), debugScript(p.Script, p.parser.eln, p.parser.ecn)))
		return
	}
	p.parseOK = true
	logInfo(fmt.Sprintf("Parse successful for %s.", so))
	logInfo(fmt.Sprintf("Found %d measurements and %d filters.", len(p.parser.measurements), len(p.parser.filters)))
	// A ProcFilter has an associated goroutine that will refresh some values at a High Frequency .
	// (faster than the frequency that telegraf calls the plugin)
	if len(p.parser.measurements) > 0 {
		p.newSample() // Make sure the 0 sample stamp value is never used for a real sample.
		if p.Netlink {
			// Setup a Netlink socket to get process events directly from the Linux kernel (better than sampling /proc).
			go getProcEvents(p)
			// Now that the envent handlers are in place, init our state with a scan of all current processes.
			scanPIDs(p)

			if p.netlinkOk {
				// Get information about processes faster than the telegraf gather loop rate.
				go updateProcstats(p)
				logInfo("Started the kernel Netlink event handlers and fast stat update goroutines.")
			} else {
				logWarning("Gathering data without Netlink and fast update. This is less accurate for short lived processes. Remember that you need root permissions for Netlink mode.")
			}
		} else {
			logWarning("Gathering data without Netlink and fast update. This is less accurate for short lived processes but it does not require root permissions and may use less resources.")
		}
	}
}

// The usual (low frequency) telegraf gather call (collect metrics and push points.
func (pf *ProcFilter) Gather(acc telegraf.Accumulator) error {
	trace("Gather %d\n", time.Now().UnixNano())
	if pf.parser == nil {
		// Only once.
		pf.init()
	}
	parser := pf.parser
	if !pf.parseOK || len(parser.measurements) == 0 {
		// Parse failed or nothing to output.
		return nil
	}
	// Processing of fork envent has been delayed. Process the fork without exec now.
	processOldForks()
	// Change the current stamp and update all global variables.
	pf.newSample()
	resetGlobalStatSets()
	if !pf.netlinkOk || pf.needOneScan {
		// No netlink of the socket had a transiant error and we need a reset of the PIDs state.
		scanPIDs(pf)
	}
	//apsDisplay()
	for _, f := range parser.filters {
		apsMutex.Lock()
		err := f.Apply()
		if err != nil {
			apsMutex.Unlock()
			logErr(err.Error())
			continue
		}
		apsMutex.Unlock()
	}
	if acc != nil {
		// Called from telelgraf => push measurements onto the accumulator.
		for _, m := range parser.measurements {
			m.push(pf, acc)
		}
	} else {
		// Called from a bench/test => display measurements.
		pf.displayMeasurements()
	}
	trace(apsStats())
	clearOldProcStats() // remove the PIDs with a n-1 timestamp.
	return nil
}

func (p *ProcFilter) displayMeasurements() error {
	parser := p.parser
	for _, m := range parser.measurements {
		fmt.Printf("%s\n", m.name)
		iStats := m.f.Stats()
		for _, ps := range iStats.pid2Stat {
			tags, err := m.getTags(ps, p.Tag_prefix)
			if err != nil {
				return err
			}
			fields, err := m.getFields(ps, p.Field_prefix)
			if err != nil {
				return err
			}
			// Display the results.
			for n, v := range tags {
				fmt.Printf("  %s=%s\n", n, v)
			}
			for n, v := range fields {
				fmt.Printf("  %s=%v\n", n, v)
			}
		}
		fmt.Println()
	}
	return nil
}

// Change the stamp for a new sample (thus disabling all previous values from last sample).
// Store the new sample start time and compute the last interval duration.
func (p *ProcFilter) newSample() {
	p.prevSampleStart = p.sampleStart
	p.sampleStart = uint64(time.Now().UnixNano())
	if p.prevSampleStart != 0 {
		p.sampleDuration = uint64(p.sampleStart - p.prevSampleStart)
		p.sampleDurationS = float32(p.sampleDuration) / 1e9 // ns to s
	}
	stamp++
	if stamp >= 254 { // uint8
		stamp = 2 // 0 and 1 are reserved as markers for unitialized and first stamp.
	}
}
