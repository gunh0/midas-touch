package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const apiBaseURL = "https://api.telegram.org/bot"

type Client struct {
	token      string
	chatID     string
	httpClient *http.Client
}

type sendMessageRequest struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

type apiResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
}

func NewClient(token, chatID string) *Client {
	return &Client{
		token:  token,
		chatID: chatID,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// SendMessage sends a plain text message to the configured chat.
func (c *Client) SendMessage(text string) error {
	return c.sendMessage(text, "")
}

// SendMarkdown sends a message with Markdown V2 formatting.
func (c *Client) SendMarkdown(text string) error {
	return c.sendMessage(text, "MarkdownV2")
}

func (c *Client) sendMessage(text, parseMode string) error {
	payload := sendMessageRequest{
		ChatID:    c.chatID,
		Text:      text,
		ParseMode: parseMode,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s%s/sendMessage", apiBaseURL, c.token)
	resp, err := c.httpClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	var result apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if !result.OK {
		return fmt.Errorf("telegram api error: %s", result.Description)
	}

	return nil
}
