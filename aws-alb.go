package main

import (
	"errors"
	"flag"
	"log"
	"os"
	"time"

	"github.com/crowdmob/goamz/aws"
	"github.com/crowdmob/goamz/cloudwatch"
	mp "github.com/mackerelio/go-mackerel-plugin"
)

var graphdef = map[string](mp.Graphs){
	"alb.targetresponsetime": mp.Graphs{
		Label: "Whole ALB TargetResponseTime",
		Unit:  "float",
		Metrics: [](mp.Metrics){
			mp.Metrics{Name: "TargetResponseTime", Label: "TargetResponseTime"},
		},
	},
	"alb.http_target": mp.Graphs{
		Label: "Whole ALB HTTP Target Count",
		Unit:  "integer",
		Metrics: [](mp.Metrics){
			mp.Metrics{Name: "HTTPCode_Target_2XX_Count", Label: "2XX", Stacked: true},
			mp.Metrics{Name: "HTTPCode_Target_3XX_Count", Label: "3XX", Stacked: true},
			mp.Metrics{Name: "HTTPCode_Target_4XX_Count", Label: "4XX", Stacked: true},
			mp.Metrics{Name: "HTTPCode_Target_5XX_Count", Label: "5XX", Stacked: true},
		},
	},
	// "alb.healthy_host_count", "alb.unhealthy_host_count" will be generated dynamically
}

type statType int

const (
	stAve statType = iota
	stSum
)

func (s statType) String() string {
	switch s {
	case stAve:
		return "Average"
	case stSum:
		return "Sum"
	}
	return ""
}

// ALBPlugin alb plugin for mackerel
type ALBPlugin struct {
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	AZs             []string
	CloudWatch      *cloudwatch.CloudWatch
	Lbname          string
	Tgname          string
}

func (p *ALBPlugin) prepare() error {
	auth, err := aws.GetAuth(p.AccessKeyID, p.SecretAccessKey, "", time.Now())
	if err != nil {
		return err
	}

	p.CloudWatch, err = cloudwatch.NewCloudWatch(auth, aws.Regions[p.Region].CloudWatchServicepoint)
	if err != nil {
		return err
	}

	// Fetch AvailabilityZone From Namespace: AWS/ELB
	ret, err := p.CloudWatch.ListMetrics(&cloudwatch.ListMetricsRequest{
		Namespace: "AWS/ELB",
		Dimensions: []cloudwatch.Dimension{
			{
				Name: "AvailabilityZone",
			},
		},
		MetricName: "HealthyHostCount",
	})

	if err != nil {
		return err
	}

	p.AZs = make([]string, 0, len(ret.ListMetricsResult.Metrics))
	for _, met := range ret.ListMetricsResult.Metrics {
		if len(met.Dimensions) > 1 {
			continue
		} else if met.Dimensions[0].Name != "AvailabilityZone" {
			continue
		}

		p.AZs = append(p.AZs, met.Dimensions[0].Value)
	}

	return nil
}

func (p ALBPlugin) getLastPoint(dimensions *[]cloudwatch.Dimension, metricName string, sTyp statType) (float64, error) {
	now := time.Now()

	response, err := p.CloudWatch.GetMetricStatistics(&cloudwatch.GetMetricStatisticsRequest{
		Dimensions: *dimensions,
		StartTime:  now.Add(time.Duration(120) * time.Second * -1), // 2 min (to fetch at least 1 data-point)
		EndTime:    now,
		MetricName: metricName,
		Period:     60,
		Statistics: []string{sTyp.String()},
		Namespace:  "AWS/ApplicationELB",
	})
	if err != nil {
		return 0, err
	}

	datapoints := response.GetMetricStatisticsResult.Datapoints
	if len(datapoints) == 0 {
		return 0, errors.New("fetched no datapoints")
	}

	latest := time.Unix(0, 0)
	var latestVal float64
	for _, dp := range datapoints {
		if dp.Timestamp.Before(latest) {
			continue
		}

		latest = dp.Timestamp
		switch sTyp {
		case stAve:
			latestVal = dp.Average
		case stSum:
			latestVal = dp.Sum
		}
	}

	return latestVal, nil
}

// FetchMetrics fetch alb metrics
func (p ALBPlugin) FetchMetrics() (map[string]float64, error) {
	stat := make(map[string]float64)

	// HostCount per AZ
	for _, az := range p.AZs {
		d := []cloudwatch.Dimension{
			{
				Name:  "AvailabilityZone",
				Value: az,
			},
		}
		if p.Lbname != "" {
			d2 := cloudwatch.Dimension{
				Name:  "LoadBalancer",
				Value: p.Lbname,
			}
			d = append(d, d2)
		}
		if p.Tgname != "" {
			d3 := cloudwatch.Dimension{
				Name:  "TargetGroup",
				Value: p.Tgname,
			}
			d = append(d, d3)
		}

		for _, met := range []string{"HealthyHostCount", "UnHealthyHostCount"} {
			v, err := p.getLastPoint(&d, met, stAve)
			if err == nil {
				stat[met+"_"+az] = v
			}
		}
	}

	glb := []cloudwatch.Dimension{}
	if p.Lbname != "" {
		g2 := cloudwatch.Dimension{
			Name:  "LoadBalancer",
			Value: p.Lbname,
		}
		glb = append(glb, g2)
	}
	if p.Tgname != "" {
		g3 := cloudwatch.Dimension{
			Name:  "TargetGroup",
			Value: p.Tgname,
		}
		glb = append(glb, g3)
	}

	v, err := p.getLastPoint(&glb, "TargetResponseTime", stAve)
	if err == nil {
		stat["TargetResponseTime"] = v
	}

	for _, met := range [...]string{"HTTPCode_Target_2XX_Count", "HTTPCode_Target_3XX_Count", "HTTPCode_Target_4XX_Count", "HTTPCode_Target_5XX_Count"} {
		v, err := p.getLastPoint(&glb, met, stSum)
		if err == nil {
			stat[met] = v
		}
	}

	return stat, nil
}

// GraphDefinition for Mackerel
func (p ALBPlugin) GraphDefinition() map[string](mp.Graphs) {
	for _, grp := range [...]string{"alb.healthy_host_count", "alb.unhealthy_host_count"} {
		var namePre string
		var label string
		switch grp {
		case "alb.healthy_host_count":
			namePre = "HealthyHostCount_"
			label = "ALB Healthy Host Count"
		case "alb.unhealthy_host_count":
			namePre = "UnHealthyHostCount_"
			label = "ALB Unhealthy Host Count"
		}

		var metrics [](mp.Metrics)
		for _, az := range p.AZs {
			metrics = append(metrics, mp.Metrics{Name: namePre + az, Label: az, Stacked: true})
		}
		graphdef[grp] = mp.Graphs{
			Label:   label,
			Unit:    "integer",
			Metrics: metrics,
		}
	}

	return graphdef
}

func main() {
	optRegion := flag.String("region", "", "AWS Region")
	optLbname := flag.String("lbname", "", "ALB Name")
	optTgname := flag.String("tgname", "", "TargetGroup Name")
	optAccessKeyID := flag.String("access-key-id", "", "AWS Access Key ID")
	optSecretAccessKey := flag.String("secret-access-key", "", "AWS Secret Access Key")
	optTempfile := flag.String("tempfile", "", "Temp file name")
	flag.Parse()

	var alb ALBPlugin

	if *optRegion == "" {
		alb.Region = aws.InstanceRegion()
	} else {
		alb.Region = *optRegion
	}

	alb.AccessKeyID = *optAccessKeyID
	alb.SecretAccessKey = *optSecretAccessKey
	alb.Lbname = *optLbname
	alb.Tgname = *optTgname

	err := alb.prepare()
	if err != nil {
		log.Fatalln(err)
	}

	helper := mp.NewMackerelPlugin(alb)
	if *optTempfile != "" {
		helper.Tempfile = *optTempfile
	} else {
		helper.Tempfile = "/tmp/mackerel-plugin-alb"
	}

	if os.Getenv("MACKEREL_AGENT_PLUGIN_META") != "" {
		helper.OutputDefinitions()
	} else {
		helper.OutputValues()
	}
}
