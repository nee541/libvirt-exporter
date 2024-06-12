package collector

import (
	"sync"

	libvirt "github.com/digitalocean/go-libvirt"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
)

type blockCollector struct {
	readBytes     typedDesc
	readRequests  typedDesc
	writeBytes    typedDesc
	writeRequests typedDesc
	logger        log.Logger
}

const blockSubsystemName = "domain_block"

func init() {
	registerCollector("block", defaultEnabled, NewBlockCollector)
}

func NewBlockCollector(logger log.Logger) (Collector, error) {
	return &blockCollector{
		readBytes: typedDesc{
			desc: prometheus.NewDesc(
				prometheus.BuildFQName(namespace, blockSubsystemName, "read_bytes_total"),
				"Total number of bytes read from a block device",
				[]string{"domain_uuid", "source_file", "target_device"},
				nil),
			valueType: prometheus.CounterValue,
		},
		readRequests: typedDesc{
			desc: prometheus.NewDesc(
				prometheus.BuildFQName(namespace, blockSubsystemName, "read_requests_total"),
				"Total number of read requests made to a block device",
				[]string{"domain_uuid", "source_file", "target_device"},
				nil),
			valueType: prometheus.CounterValue,
		},
		writeBytes: typedDesc{
			desc: prometheus.NewDesc(
				prometheus.BuildFQName(namespace, blockSubsystemName, "write_bytes_total"),
				"Total number of bytes written to a block device",
				[]string{"domain_uuid", "source_file", "target_device"},
				nil),
			valueType: prometheus.CounterValue,
		},
		writeRequests: typedDesc{
			desc: prometheus.NewDesc(
				prometheus.BuildFQName(namespace, blockSubsystemName, "write_requests_total"),
				"Total number of write requests made to a block device",
				[]string{"domain_uuid", "source_file", "target_device"},
				nil),
			valueType: prometheus.CounterValue,
		},

		logger: logger,
	}, nil
}

func (c *blockCollector) Update(ch chan<- prometheus.Metric, opts ...CollectorOption) error {
	config := &CollectorConfig{}
	for _, opt := range opts {
		opt(config)
	}

	if config.pLibvirt == nil {
		level.Error(c.logger).Log("msg", "libvirt not created")
		return ErrNotProvided
	}
	if !config.pLibvirt.IsConnected() {
		level.Error(c.logger).Log("msg", "libvirt not connected")
		return ErrNotProvided
	}
	if config.lvDomains == nil || len(config.lvDomains) == 0 {
		level.Error(c.logger).Log("msg", "no domains found")
		return ErrNotProvided
	}
	pLibvirt := config.pLibvirt
	lvDomains := config.lvDomains

	wgCounter := 0
	for _, lvDomain := range lvDomains {
		wgCounter += len(lvDomain.Schema.Devices.Disks)
	}
	wg := sync.WaitGroup{}
	wg.Add(wgCounter)
	for _, lvDomain := range lvDomains {
		for _, disk := range lvDomain.Schema.Devices.Disks {
			if disk.Device == "cdrom" || disk.Device == "fq" {
				// skip cdrom and floppy disk
				// Decrease the wait group counter to avoid deadlock
				wg.Done()
				continue
			}
			domainUUID := lvDomain.Schema.UUID
			sourceFile := disk.Source.File
			targetDevice := disk.Target.Device

			go func(domain libvirt.Domain, domainUUID, sourceFile, targetDevice string) {
				rRdReq, rRdBytes, rWrReq, rWrBytes, _, err := pLibvirt.DomainBlockStats(domain, targetDevice)
				if err != nil {
					level.Error(c.logger).Log("msg", "failed to get block stats", "domain", domain.Name, "err", err)
					wg.Done()
					return
				}
				level.Debug(c.logger).Log("msg", "get block stats", "domain", domain.Name, "rRdReq", rRdReq, "rRdBytes", rRdBytes, "rWrReq", rWrReq, "rWrBytes", rWrBytes)
				ch <- c.readBytes.mustNewConstMetric(float64(rRdBytes), domainUUID, sourceFile, targetDevice)
				ch <- c.readRequests.mustNewConstMetric(float64(rRdReq), domainUUID, sourceFile, targetDevice)
				ch <- c.writeBytes.mustNewConstMetric(float64(rWrBytes), domainUUID, sourceFile, targetDevice)
				ch <- c.writeRequests.mustNewConstMetric(float64(rWrReq), domainUUID, sourceFile, targetDevice)

				// Task finished, decrease the wait group counter
				wg.Done()
			}(lvDomain.Domain, domainUUID, sourceFile, targetDevice)
		}
	}

	wg.Wait()

	return nil
}
