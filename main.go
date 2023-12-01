package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"time"
	"tinygo.org/x/bluetooth"
)

type Packet struct {
	PktLength int16 `json:"-"`
	DataType  byte  `json:"dataType"`
	Head      byte  `json:"-"`

	TargetStatus         byte  `json:"targetStatus"`
	MovingTargetDistance int16 `json:"movingTargetDistance"`
	MovingTargetEnergy   int8  `json:"movingTargetEnergy"`
	StaticTargetDistance int16 `json:"staticTargetDistance"`
	StaticTargetEnergy   int8  `json:"staticTargetEnergy"`
	FindDistance         int16 `json:"findDistance"`

	Tail     byte `json:"-"`
	Checksum byte `json:"-"`
}

var adapter = bluetooth.DefaultAdapter

const DeviceMac = "B7:04:84:25:19:4C"
const ServiceId = "0000fff0-0000-1000-8000-00805f9b34fb"
const NotifyCharId = "0000fff1-0000-1000-8000-00805f9b34fb"
const WriteCharId = "0000fff2-0000-1000-8000-00805f9b34fb"

func createMqttClient() mqtt.Client {
	const ConnectAddress = "tcp://192.168.31.241:1883"
	const ClientId = "503-scanner-mqtt"
	fmt.Println("MQTT Connect Address: ", ConnectAddress)
	opts := mqtt.NewClientOptions()
	opts.AddBroker(ConnectAddress)
	opts.SetUsername("")
	opts.SetPassword("")
	opts.SetClientID(ClientId)
	opts.SetKeepAlive(time.Second * 60)
	client := mqtt.NewClient(opts)
	token := client.Connect()

	if token.WaitTimeout(5*time.Second) && token.Error() != nil {
		must("MQTT server connect", token.Error())
	}
	return client
}

func main() {
	println("503 scanner starting...")

	must("Enable BLE stack", adapter.Enable())

	ch := make(chan bluetooth.ScanResult, 1)

	println("Scanning target BLE devices")
	err := adapter.Scan(func(adapter *bluetooth.Adapter, result bluetooth.ScanResult) {
		println("Found device: ", result.Address.String(), result.RSSI, result.LocalName())
		if result.Address.String() == DeviceMac {
			err := adapter.StopScan()
			if err != nil {
				println("BLE Stop scan error")
				return
			}
			ch <- result
		}
	})

	var device *bluetooth.Device
	select {
	case result := <-ch:
		device, err = adapter.Connect(result.Address, bluetooth.ConnectionParams{})
		if err != nil {
			println(err.Error())
			return
		}
		println("Connected to ", result.Address.String(), result.LocalName())
	}
	defer func(device *bluetooth.Device) {
		err := device.Disconnect()
		if err != nil {
			println("Error disconnect BLE device. Ignoring.")
		}
	}(device)

	mqttClient := createMqttClient()
	defer mqttClient.Disconnect(500)

	println("Discovering Services")
	ble_services, err := device.DiscoverServices(nil)
	must("Discover services", err)

	var radar_serv bluetooth.DeviceService
	for _, service := range ble_services {
		if service.String() == ServiceId {
			radar_serv = service
			println("Found target service")
			break
		}
	}

	radar_char, err := radar_serv.DiscoverCharacteristics(nil)
	must("Error discovering characteristics", err)

	var (
		command_char bluetooth.DeviceCharacteristic
		notify_char  bluetooth.DeviceCharacteristic
	)

	for _, characteristic := range radar_char {
		switch characteristic.String() {
		case NotifyCharId:
			notify_char = characteristic
			println("Found notify char")
		case WriteCharId:
			println("Found command char")
			command_char = characteristic
		}
	}

	// Write login
	messageChan := make(chan []byte, 10)
	err = notify_char.EnableNotifications(func(buf []byte) {
		select {
		case messageChan <- buf:
		default:
			println("Error: Buffer full")
		}
	})
	must("Failed to enable notify", err)

	_, err = command_char.WriteWithoutResponse([]byte{0xfd, 0xfc, 0xfb, 0xfa, 0x08, 0x00, 0xa8, 0x00, 0x48, 0x69, 0x4c,
		0x69, 0x6e, 0x6b, 0x04, 0x03, 0x02, 0x01})
	must("Error writing command", err)

	for {
		select {
		case message := <-messageChan:
			if !bytes.Equal(message[:4], []byte{0xf4, 0xf3, 0xf2, 0xf1}) || !bytes.Equal(message[len(message)-4:], []byte{0xf8, 0xf7, 0xf6, 0xf5}) {
				fmt.Printf("Bad packetArray received, ignoring [% x]", message)
				continue
			}
			packetArray := message[4 : len(message)-4]
			//fmt.Printf("% x\n", packetArray)
			pktBuffer := bytes.NewBuffer(packetArray)
			var pkt Packet
			err := binary.Read(pktBuffer, binary.LittleEndian, &pkt)
			if err != nil {
				println("Error reading packet.")
				continue
			}
			//println("Packet length ", pkt.PktLength)
			if pkt.Head != 0xAA {
				continue
			}

			if pkt.DataType != 0x02 {
				println("Ignoring engineering data")
				continue
			}

			switch pkt.TargetStatus {
			case 0x00:
				println("No target ")
			case 0x01:
				println("Moving target @ ", pkt.MovingTargetDistance, "cm")
			case 0x02:
				println("Static target @ ", pkt.StaticTargetDistance, "cm")
			case 0x03:
				println("Moving(", pkt.MovingTargetDistance, "cm)&static target(", pkt.StaticTargetDistance, "cm)")
			}

			jsonData, err := json.Marshal(pkt)
			if err != nil {
				return
			}

			// Publish message
			mqttClient.Publish("lot/503radar", 0, false, string(jsonData))
		}
	}
}

func must(action string, err error) {
	if err != nil {
		panic("Failed to " + action + ": Aborting...")
	}
}
