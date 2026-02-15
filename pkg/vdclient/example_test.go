package vdclient_test

import (
	"context"
	"fmt"

	"github.com/realnikolaj/voicedaemon/pkg/vdclient"
)

func ExampleClient_Speak() {
	client := vdclient.New("http://localhost:5111")
	resp, err := client.Speak(context.Background(), vdclient.SpeakRequest{
		Text:    "Hello from osog",
		Backend: "speaches",
		Voice:   "af_heart",
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("status:", resp.Status)
}

func ExampleClient_Health() {
	client := vdclient.New("http://localhost:5111")
	resp, err := client.Health(context.Background())
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("status:", resp.Status)
}

func ExampleDial() {
	session, err := vdclient.Dial("/tmp/voice-daemon.sock")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer func() {
		if err := session.Close(); err != nil {
			fmt.Println("close error:", err)
		}
	}()

	if err := session.Start(); err != nil {
		fmt.Println("start error:", err)
		return
	}

	for transcript := range session.Transcripts() {
		fmt.Println("heard:", transcript)
	}

	full, err := session.Stop()
	if err != nil {
		fmt.Println("stop error:", err)
		return
	}
	fmt.Println("full:", full)
}
