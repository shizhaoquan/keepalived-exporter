package collector

import (
	"strconv"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"github.com/sparrc/go-ping"
)

// KeepalivedCollector implements prometheus.Collector interface and stores required info to collect data
type KeepalivedCollector struct {
	sync.Mutex
	useJSON  bool
	ping     bool
	pidPath  string
	SIGDATA  int
	SIGJSON  int
	SIGSTATS int
	metrics  map[string]*prometheus.Desc
}

// VRRPStats represents Keepalived stats about VRRP
type VRRPStats struct {
	AdvertRcvd        int `json:"advert_rcvd"`
	AdvertSent        int `json:"advert_sent"`
	BecomeMaster      int `json:"become_master"`
	ReleaseMaster     int `json:"release_master"`
	PacketLenErr      int `json:"packet_len_err"`
	AdvertIntervalErr int `json:"advert_interval_err"`
	IPTTLErr          int `json:"ip_ttl_err"`
	InvalidTypeRcvd   int `json:"invalid_type_rcvd"`
	AddrListErr       int `json:"addr_list_err"`
	InvalidAuthType   int `json:"invalid_authtype"`
	AuthTypeMismatch  int `json:"authtype_mismatch"`
	AuthFailure       int `json:"auth_failure"`
	PRIZeroRcvd       int `json:"pri_zero_rcvd"`
	PRIZeroSent       int `json:"pri_zero_sent"`
}

// VRRPData represents Keepalived data about VRRP
type VRRPData struct {
	IName     string   `json:"iname"`
	State     int      `json:"state"`
	WantState int      `json:"wantstate"`
	Intf      string   `json:"ifp_ifname"`
	GArpDelay int      `json:"garp_delay"`
	VRID      int      `json:"vrid"`
	VIPs      []string `json:"vips"`
}

// VRRPScript represents Keepalived script about VRRP
type VRRPScript struct {
	Name   string
	Status string
	State  string
}

// VRRP ties together VRRPData and VRRPStats
type VRRP struct {
	Data  VRRPData  `json:"data"`
	Stats VRRPStats `json:"stats"`
}

// KeepalivedStats ties together VRRP and VRRPScript
type KeepalivedStats struct {
	VRRPs   []VRRP
	Scripts []VRRPScript
}

// NewKeepalivedCollector is creating new instance of KeepalivedCollector
func NewKeepalivedCollector(useJSON, ping bool, pidPath string) *KeepalivedCollector {
	kc := &KeepalivedCollector{
		useJSON: useJSON,
		ping:    ping,
		pidPath: pidPath,
	}

	commonLabels := []string{"iname", "intf", "vrid", "state"}
	kc.metrics = map[string]*prometheus.Desc{
		"keepalived_up":                  prometheus.NewDesc("keepalived_up", "Status", nil, nil),
		"keepalived_vrrp_state":          prometheus.NewDesc("keepalived_vrrp_state", "State of vrrp", []string{"iname", "intf", "vrid", "ip_address"}, nil),
		"keepalived_ping_packet_loss":    prometheus.NewDesc("keepalived_ping_packet_loss", "Ping packet loss status to each vrrp", []string{"iname", "intf", "vrid", "ip_address"}, nil),
		"keepalived_garp_delay":          prometheus.NewDesc("keepalived_garp_deplay", "Gratuitous ARP delay", commonLabels, nil),
		"keepalived_advert_rcvd":         prometheus.NewDesc("keepalived_advert_rcvd", "Advertisements received", commonLabels, nil),
		"keepalived_advert_sent":         prometheus.NewDesc("keepalived_advert_sent", "Advertisements sent", commonLabels, nil),
		"keepalived_become_master":       prometheus.NewDesc("keepalived_become_master", "Became master", commonLabels, nil),
		"keepalived_release_master":      prometheus.NewDesc("keepalived_release_master", "Released master", commonLabels, nil),
		"keepalived_packet_len_err":      prometheus.NewDesc("keepalived_packet_len_err", "Packet length errors", commonLabels, nil),
		"keepalived_advert_interval_err": prometheus.NewDesc("keepalived_advert_interval_err", "Advertisement interval errors", commonLabels, nil),
		"keepalived_ip_ttl_err":          prometheus.NewDesc("keepalived_ip_ttl_err", "TTL errors", commonLabels, nil),
		"keepalived_invalid_type_rcvd":   prometheus.NewDesc("keepalived_invalid_type_rcvd", "Invalid type errors", commonLabels, nil),
		"keepalived_addr_list_err":       prometheus.NewDesc("keepalived_addr_list_err", "Address list errors", commonLabels, nil),
		"keepalived_invalid_authtype":    prometheus.NewDesc("keepalived_invalid_authtype", "Authentication invalid", commonLabels, nil),
		"keepalived_authtype_mismatch":   prometheus.NewDesc("keepalived_authtype_mismatch", "Authentication mismatch", commonLabels, nil),
		"keepalived_auth_failure":        prometheus.NewDesc("keepalived_auth_failure", "Authentication failure", commonLabels, nil),
		"keepalived_pri_zero_rcvd":       prometheus.NewDesc("keepalived_pri_zero_rcvd", "Priority zero received", commonLabels, nil),
		"keepalived_pri_zero_sent":       prometheus.NewDesc("keepalived_pri_zero_sent", "Priority zero sent", commonLabels, nil),
		"keepalived_script_status":       prometheus.NewDesc("keepalived_script_status", "Tracker Script Status", []string{"name"}, nil),
		"keepalived_script_state":        prometheus.NewDesc("keepalived_script_state", "Tracker Script State", []string{"name"}, nil),
	}

	kc.SIGJSON = sigNum("JSON")
	kc.SIGDATA = sigNum("DATA")
	kc.SIGSTATS = sigNum("STATS")

	return kc
}

func (k *KeepalivedCollector) newConstMetric(ch chan<- prometheus.Metric, name string, value float64, lableValues ...string) {
	// TODO: Why constMetric?
	pm, err := prometheus.NewConstMetric(
		k.metrics[name],
		prometheus.GaugeValue,
		value,
		lableValues...,
	)
	if err != nil {
		logrus.Errorf("Failed to register %q metric: %s", name, err)
		return
	}

	ch <- pm
}

// Collect get metrics and add to prometheus metric channel
func (k *KeepalivedCollector) Collect(ch chan<- prometheus.Metric) {
	k.Lock()
	defer k.Unlock()

	keepalivedUp := float64(1)

	keepalivedStats, err := k.stats()
	if err != nil {
		logrus.WithField("json", k.useJSON).Error("No data found to be exported: ", err)
		keepalivedUp = 0
	}

	k.newConstMetric(ch, "keepalived_up", keepalivedUp)

	if keepalivedUp == 0 {
		return
	}

	for _, vrrp := range keepalivedStats.VRRPs {
		state := ""
		ok := false
		if state, ok = vrrp.Data.getStringState(vrrp.Data.State); !ok {
			logrus.WithField("state", vrrp.Data.State).Warn("Unknown State found for vrrp: ", vrrp.Data.IName)
		}

		k.newConstMetric(ch, "keepalived_advert_rcvd", float64(vrrp.Stats.AdvertRcvd), vrrp.Data.IName, vrrp.Data.Intf, strconv.Itoa(vrrp.Data.VRID), state)
		k.newConstMetric(ch, "keepalived_advert_sent", float64(vrrp.Stats.AdvertSent), vrrp.Data.IName, vrrp.Data.Intf, strconv.Itoa(vrrp.Data.VRID), state)
		k.newConstMetric(ch, "keepalived_become_master", float64(vrrp.Stats.BecomeMaster), vrrp.Data.IName, vrrp.Data.Intf, strconv.Itoa(vrrp.Data.VRID), state)
		k.newConstMetric(ch, "keepalived_release_master", float64(vrrp.Stats.ReleaseMaster), vrrp.Data.IName, vrrp.Data.Intf, strconv.Itoa(vrrp.Data.VRID), state)
		k.newConstMetric(ch, "keepalived_packet_len_err", float64(vrrp.Stats.PacketLenErr), vrrp.Data.IName, vrrp.Data.Intf, strconv.Itoa(vrrp.Data.VRID), state)
		k.newConstMetric(ch, "keepalived_advert_interval_err", float64(vrrp.Stats.AdvertIntervalErr), vrrp.Data.IName, vrrp.Data.Intf, strconv.Itoa(vrrp.Data.VRID), state)
		k.newConstMetric(ch, "keepalived_ip_ttl_err", float64(vrrp.Stats.IPTTLErr), vrrp.Data.IName, vrrp.Data.Intf, strconv.Itoa(vrrp.Data.VRID), state)
		k.newConstMetric(ch, "keepalived_invalid_type_rcvd", float64(vrrp.Stats.InvalidTypeRcvd), vrrp.Data.IName, vrrp.Data.Intf, strconv.Itoa(vrrp.Data.VRID), state)
		k.newConstMetric(ch, "keepalived_addr_list_err", float64(vrrp.Stats.AddrListErr), vrrp.Data.IName, vrrp.Data.Intf, strconv.Itoa(vrrp.Data.VRID), state)
		k.newConstMetric(ch, "keepalived_invalid_authtype", float64(vrrp.Stats.InvalidAuthType), vrrp.Data.IName, vrrp.Data.Intf, strconv.Itoa(vrrp.Data.VRID), state)
		k.newConstMetric(ch, "keepalived_authtype_mismatch", float64(vrrp.Stats.AuthFailure), vrrp.Data.IName, vrrp.Data.Intf, strconv.Itoa(vrrp.Data.VRID), state)
		k.newConstMetric(ch, "keepalived_auth_failure", float64(vrrp.Stats.AuthFailure), vrrp.Data.IName, vrrp.Data.Intf, strconv.Itoa(vrrp.Data.VRID), state)
		k.newConstMetric(ch, "keepalived_pri_zero_rcvd", float64(vrrp.Stats.PRIZeroRcvd), vrrp.Data.IName, vrrp.Data.Intf, strconv.Itoa(vrrp.Data.VRID), state)
		k.newConstMetric(ch, "keepalived_pri_zero_sent", float64(vrrp.Stats.PRIZeroSent), vrrp.Data.IName, vrrp.Data.Intf, strconv.Itoa(vrrp.Data.VRID), state)
		k.newConstMetric(ch, "keepalived_garp_delay", float64(vrrp.Data.GArpDelay), vrrp.Data.IName, vrrp.Data.Intf, strconv.Itoa(vrrp.Data.VRID), state)

		for _, ip := range vrrp.Data.VIPs {
			ipAddr := strings.Split(ip, " ")[0]
			intf := strings.Split(ip, " ")[2]

			k.newConstMetric(ch, "keepalived_vrrp_state", float64(vrrp.Data.State), vrrp.Data.IName, intf, strconv.Itoa(vrrp.Data.VRID), ipAddr)

			if k.ping {
				pingResult, err := pingVIP(ipAddr)
				if err != nil {
					logrus.WithField("VIP", ipAddr).Error("Faild to ping: ", err)
					continue
				}

				k.newConstMetric(ch, "keepalived_ping_packet_loss", pingResult.PacketLoss, vrrp.Data.IName, intf, strconv.Itoa(vrrp.Data.VRID), ipAddr)
			}
		}
	}

	for _, script := range keepalivedStats.Scripts {
		if scriptStatus, ok := script.getIntStatus(script.Status); !ok {
			logrus.WithFields(logrus.Fields{"status": script.Status, "name": script.Name}).Warn("Unknown status")
		} else {
			k.newConstMetric(ch, "keepalived_script_status", float64(scriptStatus), script.Name)
		}

		if scriptState, ok := script.getIntState(script.State); !ok {
			logrus.WithFields(logrus.Fields{"state": script.State, "name": script.Name}).Warn("Unknown state")
		} else {
			k.newConstMetric(ch, "keepalived_script_state", float64(scriptState), script.Name)
		}
	}
}

func pingVIP(ipAddr string) (*ping.Statistics, error) {
	pinger, err := ping.NewPinger(ipAddr)
	if err != nil {
		return nil, err
	}
	pinger.SetPrivileged(true)
	pinger.Count = 1
	pinger.Run()
	return pinger.Statistics(), nil
}

// Describe outputs metrics descriptions
func (k *KeepalivedCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range k.metrics {
		ch <- m
	}
}
