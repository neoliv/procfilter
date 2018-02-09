// parse project main.go
package procfilter

// DEBUG/OPTIM
/*import (
	"log"
	"net/http"
	_ "net/http/pprof"
)*/

import (
	"fmt"
	"io/ioutil"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/process"
	//"encoding/json"
)

var testScripts = []string{
	`pbu <- packby(user, 'oliv', 'root', all)
     pbu = tag(user) fields(process_nb) <- pbu
`,
	`bycmd <- packby(cmd)
	 top.cmd.cpu = tag(cmd) field(cpu,rss,vsz,swap) <- top(cpu,3,bycmd)
	 top.cmd.rss = tag(cmd) field(cpu,rss,vsz,swap) <- top(rss,3,bycmd)
	 top.cmd.process_nb = tag(cmd) field(process_nb,cpu,rss,vsz,swap) <- top(process_nb,3,bycmd)`,
	//`child = tag(cmd) field(process_nb,cpu) <-
	//`top.cmd.cpu = tag(cmd) field(pid,ppid,cpu,cmd_line,rss,vsz,swap) <- top(cpu,3)`,
	//`top.cmd.rss = tag(cmd) field(rss) <- top(rss,3)`,
	//`child = tag(cmd) field(process_nb,cpu) <- pack(children(pid(0)))`,
	//`child = tag(cmd) field(process_nb,cpu) <- pack(children(cmd("Spawn"r)))`,
	//`m = tag(cmd) field(cpu) <- top(cpu,2,user('^ol'r!))`,
	/*`# Ressource hogs
	     #top.cmd.cpu = tag(cmd) field(pid,cpu,cmd_line) <- top(cpu,3)
		 stress = tag(cmd) field(pid,cpu,cmd_line) <- cmd("stress"r)

	     #top.cmd.rss = tag(cmd) field(rss) <- top(rss,3)
	     #top.cmd.swap = tag(cmd) field(cpu,rss,vsz,swap,cmd_line) <- top(swap,3)
	     #top.cmd.thread_nb = tag(cmd) field(cpu,rss,vsz,swap,thread_nb,cmd_line) <- top(thread_nb,3)

	     # Pack by user
	     #top.by.user.cpu = tag(user) field(cpu) <- top(cpu,3,by(user))
	     #top.by.user.rss = tag(user) field(rss) <- top(rss,3,by(user))
	     #top.by.user.swap = tag(user) field(swap) <- top(swap,3,by(user))
	     #top.by.user.process_nb = tag(user) field(process_nb) <- top(process_nb,3,by(user))

	     # Workloads
	     #wl.omni = field(process_nb,cpu,rss,vsz,swap) <- pack(children(or(cmd('omv_'r),user('omni'))))
	     #wl.influx = field(process_nb,cpu,rss,vsz,swap) <- pack(cmd('influxdb'r),user('influxdb'))
	     #wl.telegraf = field(process_nb,cpu,rss,vsz,swap) <- pack(user('telegraf'))
	     #wl.root = field(process_nb,cpu,rss,vsz,swap) <- pack(user(0))
	     #wl._other = field(process_nb,cpu,rss,vsz,swap) <- pack(not(filters('^wl[.]'r)))
		`,*/

	//`m = tag(user,uid) field(rss) <- by(user)`,
	//`m = field(rss) <- top(rss,2,by(user))`,
	//`top_cpu = tag(cmd) field(cpu) <- top(cpu,2)`,
	/*`np_o <- pack(user("ol"r))
	  np_r <- pack( user(0))
	  or <- filters("^np_.*"r)
	  mo = field(process_nb) <-np_o
	  mr = field(process_nb) <-np_r
	  mor = field(process_nb) <-or
	  pmor = field(process_nb) <-pack(or)`,
	*/
	//`top.rss = tag(cmd) field(rss)<-top(rss,2)`,
	//`by.user = tag(user) field(cpu,rss)<-by(user)`,
	//`exceed.cpu = tag(cmd) field(cpu)<-exceed(cpu,5)`,
	/*`
	  o_t <- top(rss,2,user('ol'r))
	  o_t = tag(cmd,exe) fields(rss) <- o_t
	  wl_unm = field(rss,cpu,swap) <- pack(not(o_t))
	  `,*/
	//`m1 = tag(cmd) fields(rss) <- top(rss,2,all)`,
	//`m1 = tag(cmd) fields(rss) <- top(rss,5,all)`,
	//`m2 = tag(cmd) fields(rss) <- exceed(rss,300000)`,
	//`g = field(rss) <- gather(user("ol"r))`,
	//`m2 = tag(cmd) fields(rss,cpu) <- top(3,rss,cmd("chrome"r))`,
	//`m2 = tag(cmd) fields(rss) <- top(rss,3,cmdline("chrome --type=renderer"r))`,
	//`m2 = tag(cmd) fields(rss) <- top(rss,3,args("renderer"r))`,
	//`m2 = tag(cmd) fields(rss) <- top(rss,3,user("ol"r))`,
	//`o<-user("oliv") mx = tag(cmd) fields(rss) <- o mx = tag(cmd) value(cpu) <- o`,
	//`# comment to remove`,
	//`mn = fields(rss) <- top(rss,2,all())`,
	//`mn = fields(rss) <- top(rss,2,all)`,
	//`mn = fields(rss) <- top(rss,2)`,
	/*`a <- top(rss,10)
	  m3 = tag(cmd) fields(rss)<-a`,*/
	//`apache = fields(cpu,rss,vss) <- cmd("apache")`,
	//`# comment to remove
	/*a <- cmd('apa.*')
	  b <- user('omni')
	  m4 = tags(name) values(nb,cpu,rss) <- a`,*/
}

func BenchmarkRead(b *testing.B) {
	c := 0
	e := 0
	for i := 0; i < b.N; i++ {
		s, err := ioutil.ReadFile("/proc/1/stat")
		if err != nil {
			e++
			continue
		}
		c += len(s)
	}
	fmt.Printf("err: %d  avg file size: %f\n", e, float64(c)/float64(b.N))

	//b.ReportAllocs()
}

func BenchmarkFastRead(b *testing.B) {
	c := 0
	e := 0
	for i := 0; i < b.N; i++ {
		s, err := fastRead("/proc/1/stat")
		if err != nil {
			e++
			continue
		}
		c += len(s)
	}
	fmt.Printf("err: %d  avg file size: %f\n", e, float64(c)/float64(b.N))

	//b.ReportAllocs()
}

func BenchmarkStat(b *testing.B) {
	gproc, _ := process.NewProcess(int32(1))
	for i := 0; i < b.N; i++ {
		gproc.Percent(0)
	}
}

func BenchmarkFastStat(b *testing.B) {
	ps := &procStat{}
	ps.pfnStat = "/proc/1/stat"
	ps.pfnStatus = "/proc/1/status"
	for i := 0; i < b.N; i++ {
		ps.updateFromStat()
	}
}

func TestScanner(*testing.T) {
	for _, conf := range testScripts {
		fmt.Printf("\nScan: \"%s\"\n", conf)
		r := strings.NewReader(conf)
		scanner := newScanner(r)
		for {
			tok, lit := scanner.scan()
			fmt.Printf("%d:%q ", tok, lit)
			if tok == tTIllegal {
				fmt.Printf("Illegal:%q\n", lit)
				break
			} else if tok == tTEOF {
				fmt.Printf("EOF\n")
				break
			}
		}
	}
}

func TestParse(*testing.T) {
	// test conf

	for _, conf := range testScripts {
		fmt.Printf("\nParsed \"%s\"\n", conf)
		r := strings.NewReader(conf)
		parser := NewParser(r)
		err := parser.Parse()
		if err != nil {
			fmt.Println(err.Error())
		}
	}
}

func TestCallers(t *testing.T) {
	tracec(1, 2, "test")
}

func TestGather(t *testing.T) {
	// test conf
	for _, conf := range testScripts {
		fmt.Printf("\nTest \"%s\"\n", conf)
		pf := NewProcFilter()
		pf.Script = conf
		for i := 1; i <= 100; i++ {
			err := pf.Gather(nil) // nil means output rather than accumulate in telegraf.
			if err != nil {
				fmt.Println(err.Error())
			}
			time.Sleep(5 * time.Second)
			fmt.Printf("Done: %d samples\n", i)
		}
	}
}
