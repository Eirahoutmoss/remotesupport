package clientsignaling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	server "github.com/eirahoutmoss/remotesupport/server/signaling"
)

type Client struct {
	conn *websocket.Conn
	url  string
}

func Dial(ctx context.Context, url string) (*Client, error) {
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, url: url}, nil
}

func (c *Client) Create(ctx context.Context) (string, int, error) {
	if c.conn == nil {
		return "", 0, errors.New("signaling: client closed")
	}
	if err := wsjson.Write(ctx, c.conn, server.Message{Type: "create"}); err != nil {
		return "", 0, err
	}
	var msg server.Message
	if err := wsjson.Read(ctx, c.conn, &msg); err != nil {
		return "", 0, err
	}
	if msg.Type == "error" {
		return "", 0, fmt.Errorf("signaling: %s", msg.Reason)
	}
	if msg.Type != "created" || msg.Code == "" {
		return "", 0, errors.New("signaling: invalid create response")
	}
	return msg.Code, msg.ExpiresIn, nil
}

func (c *Client) Join(ctx context.Context, code string) error {
	if c.conn == nil {
		return errors.New("signaling: client closed")
	}
	if err := wsjson.Write(ctx, c.conn, server.Message{Type: "join", Code: code}); err != nil {
		return err
	}
	var msg server.Message
	if err := wsjson.Read(ctx, c.conn, &msg); err != nil {
		return err
	}
	if msg.Type == "error" {
		return fmt.Errorf("signaling: %s", msg.Reason)
	}
	if msg.Type != "joined" {
		return errors.New("signaling: invalid join response")
	}
	return nil
}

func (c *Client) Read(ctx context.Context) (server.Message, error) {
	var msg server.Message
	if err := wsjson.Read(ctx, c.conn, &msg); err != nil {
		return server.Message{}, err
	}
	return msg, nil
}

func (c *Client) Approve(ctx context.Context, ok bool, reason string) error {
	return wsjson.Write(ctx, c.conn, server.Message{Type: "approve", Approved: ok, Reason: reason})
}

func (c *Client) Signal(ctx context.Context, sigKind string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return wsjson.Write(ctx, c.conn, server.Message{Type: "signal", Kind: sigKind, Payload: b})
}

func (c *Client) Close(ctx context.Context) error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close(websocket.StatusNormalClosure, "")
}
