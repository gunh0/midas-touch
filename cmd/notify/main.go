package main

import (
	"fmt"
	"log"
	"os"

	"github.com/gunh0/midas-touch/internal/telegram"
)

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")

	if token == "" || chatID == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID environment variables are required")
	}

	client := telegram.NewClient(token, chatID)

	message := "🚀 *Midas Touch* Notification\\!\n\nThis is a test message\\."
	if err := client.SendMarkdown(message); err != nil {
		log.Fatalf("failed to send message: %v", err)
	}

	fmt.Println("message sent successfully")
}
