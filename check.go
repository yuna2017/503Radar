package main

import (
	"context"
	"fmt"
	"github.com/influxdata/influxdb-client-go/v2"
	mail "github.com/xhit/go-simple-mail/v2"
	"log"
	"os"
)

func main() {
	client := influxdb2.NewClient(os.Getenv("INFLUXDB_ADDR"), os.Getenv("INFLUXDB_TOKEN"))
	defer client.Close()
	queryAPI := client.QueryAPI("yuna")
	result, err := queryAPI.Query(context.Background(), `from(bucket: "iot-bucket")
  |> range(start: -5m)
  |> filter(fn: (r) => r["_measurement"] == "radar")
  |> filter(fn: (r) => r["_field"] == "targetStatus")
  |> distinct()`)

	if err != nil {
		log.Fatal("Query failed", err)
	}
	hasZero := false
	hasNonZero := false
	for result.Next() {
		switch int(result.Record().Value().(float64)) {
		case 0:
			hasZero = true
		case 2, 3:
			hasNonZero = true
		}
	}

	if !(hasZero && hasNonZero) {
		if hasZero {
			log.Println("Empty")
		} else if hasNonZero {
			log.Println("Always has people")
		} else {
			log.Println("Sensor not working")
		}
		os.Exit(0)
	}

	result, err = queryAPI.Query(context.Background(), `from(bucket: "iot-bucket")
  |> range(start: -5m)
  |> filter(fn: (r) => r["_measurement"] == "radar")
  |> filter(fn: (r) => r["_field"] == "targetStatus")
  |> last()`)

	if err != nil {
		log.Fatal("Query failed", err)
	}

	lastStatus := int(result.Record().Value().(float64))
	msg := ""
	switch lastStatus {
	case 0:
		msg = "有人 -> 无人"
	default:
		msg = "无人 -> 有人"
	}
	SendMail(msg)
}

func SendMail(msg string) {
	from := os.Getenv("SMTP_EMAIL")
	stmpHost := os.Getenv("SMTP_HOST")

	server := mail.NewSMTPClient()
	server.Host = stmpHost
	server.Port = 465
	server.Username = from
	server.Password = os.Getenv("SMTP_PASSWORD")
	server.Authentication = mail.AuthAuto
	server.Encryption = mail.EncryptionSSLTLS

	stmpClient, err := server.Connect()
	if err != nil {
		log.Fatal(err)
	}

	email := mail.NewMSG()
	email.SetFrom(fmt.Sprintf("YUNABotNotice <%s>", from))
	email.AddTo(os.Getenv("TARGET_EMAIL"))
	email.SetSubject("503 Radar Status Update")
	email.SetBody(mail.TextPlain, "Status update to: "+msg)
	err = email.Send(stmpClient)
	if err != nil {
		println("Failed to send email via stmp client, ", err)
	}
}
