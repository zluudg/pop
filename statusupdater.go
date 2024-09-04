/*
 * Johan Stenstam, johan.stenstam@internetstiftelsen.se
 */
package main

import (
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"time"

	"github.com/dnstapir/tapir"
	"github.com/spf13/viper"
)

func (td *TemData) StatusUpdater(conf *Config, stopch chan struct{}) {

	var s = tapir.TapirFunctionStatus{
		Function:        "tapir-pop",
		FunctionID:      "random-popper",
		ComponentStatus: make(map[string]tapir.TapirComponentStatus),
	}

	//	me := td.MqttEngine
	//	if me == nil {
	//		TEMExiter("StatusUpdater: MQTT Engine not running")
	//	}

	// Create a new mqtt engine just for the statusupdater.
	me, err := tapir.NewMqttEngine("statusupdater", viper.GetString("tapir.mqtt.clientid")+"statusupdates", tapir.TapirPub, td.ComponentStatusCh, log.Default())
	if err != nil {
		TEMExiter("StatusUpdater: Error creating MQTT Engine: %v", err)
	}

	// var TemStatusCh = make(chan tapir.TemStatusUpdate, 100)
	//conf.Internal.TemStatusCh = TemStatusCh

	ticker := time.NewTicker(60 * time.Second)

	statusTopic := viper.GetString("tapir.status.topic")
	if statusTopic == "" {
		TEMExiter("StatusUpdater: MQTT status topic not set")
	}
	keyfile := viper.GetString("tapir.status.signingkey")
	if keyfile == "" {
		TEMExiter("StatusUpdater: MQTT status signing key not set")
	}
	keyfile = filepath.Clean(keyfile)
	signkey, err := tapir.FetchMqttSigningKey(statusTopic, keyfile)
	if err != nil {
		TEMExiter("StatusUpdater: Error fetching MQTT signing key for topic %s: %v", statusTopic, err)
	}

	td.Logger.Printf("StatusUpdater: Adding topic '%s' to MQTT Engine", statusTopic)
	msg, err := me.PubSubToTopic(statusTopic, signkey, nil, nil)
	if err != nil {
		TEMExiter("Error adding topic %s to MQTT Engine: %v", statusTopic, err)
	}
	td.Logger.Printf("StatusUpdater: Topic status for MQTT engine %s: %+v", me.Creator, msg)

	_, outbox, _, err := me.StartEngine()
	if err != nil {
		TEMExiter("StatusUpdater: Error starting MQTT Engine: %v", err)
	}

	log.Printf("StatusUpdater: Starting")

	var known_components = []string{"tapir-observation", "mqtt-event", "rpz", "rpz-ixfr", "rpz-inbound", "downstream-notify",
		"downstream-ixfr", "mqtt-config", "mqtt-unknown", "main-boot", "cert-status"}

	var csu tapir.ComponentStatusUpdate
	var dirty bool
	for {
		select {
		case <-ticker.C:
			if dirty {
				td.Logger.Printf("StatusUpdater: Status is dirty, publishing status update: %+v", s)
				// publish an mqtt status update
				outbox <- tapir.MqttPkg{
					Topic: statusTopic,
					Type:  "data",
					Data:  tapir.TapirMsg{TapirFunctionStatus: s},
				}
				dirty = false
			}
		case csu = <-td.ComponentStatusCh:
			log.Printf("StatusUpdater: got status update message: %v", csu)
			switch csu.Status {
			case "fail", "warn", "ok":
				log.Printf("StatusUpdater: status failure: %s", csu.Msg)
				var sur tapir.StatusUpdaterResponse
				switch {
				case slices.Contains(known_components, csu.Component):
					comp := s.ComponentStatus[csu.Component]
					comp.Status = csu.Status
					comp.Msg = csu.Msg
					switch csu.Status {
					case "fail":
						comp.NumFails++
						comp.LastFail = csu.TimeStamp
						comp.ErrorMsg = csu.Msg
					case "warn":
						comp.NumWarns++
						comp.LastWarn = csu.TimeStamp
						comp.ErrorMsg = csu.Msg
					case "ok":
						comp.NumFails = 0
						comp.NumWarns = 0
						comp.LastSuccess = csu.TimeStamp
					}
					s.ComponentStatus[csu.Component] = comp
					dirty = true
					sur.Msg = fmt.Sprintf("StatusUpdater: %s report for known component: %s", csu.Status, csu.Component)
				default:
					log.Printf("StatusUpdater: %s report for unknown component: %s", csu.Status, csu.Component)
					sur.Error = true
					sur.ErrorMsg = fmt.Sprintf("StatusUpdater: %s report for unknown component: %s", csu.Status, csu.Component)
					sur.Msg = fmt.Sprintf("StatusUpdater: known components are: %v", known_components)
				}

				if csu.Response != nil {
					csu.Response <- sur
				}

			case "status":
				log.Printf("StatusUpdater: request for status report. Response: %v", csu.Response)
				if csu.Response != nil {
					csu.Response <- tapir.StatusUpdaterResponse{
						FunctionStatus:  s,
						KnownComponents: known_components,
					}
					log.Printf("StatusUpdater: request for status report sent")
				} else {
					log.Printf("StatusUpdater: request for status report ignored due to lack of a response channel")
				}

			default:
				log.Printf("StatusUpdater: Unknown status: %s", csu.Status)
			}
		case <-stopch:
			log.Printf("StatusUpdater: stopping")
			return
		}
	}
}