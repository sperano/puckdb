package notify

import (
	"bufio"
	"context"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testTimeout = 5 * time.Second

var testDate = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func testEmail() Email {
	return Email{
		From:    "puckdb@example.com",
		To:      []string{"owner@example.com", "Second <second@example.com>"},
		Subject: "[puckdb] Yahoo app NOT authorized yet",
		Body:    "line one\nline two\n",
		Date:    testDate,
	}
}

func TestParseRecipients(t *testing.T) {
	require.Equal(t, []string{"a@example.com", "b@example.com"}, ParseRecipients(" a@example.com, ,b@example.com,"))
	require.Nil(t, ParseRecipients(""))
}

func TestEmail_Message(t *testing.T) {
	msg, err := testEmail().Message()
	require.NoError(t, err)

	// The Message-ID is random: check its shape, then compare the rest.
	messageID := regexp.MustCompile(`Message-ID: <\d+\.[0-9a-f]{32}@example\.com>\r\n`)
	require.Regexp(t, messageID, string(msg))
	msg = messageID.ReplaceAll(msg, nil)

	want := "From: <puckdb@example.com>\r\n" +
		"To: <owner@example.com>, \"Second\" <second@example.com>\r\n" +
		"Subject: [puckdb] Yahoo app NOT authorized yet\r\n" +
		"Date: Thu, 01 Oct 2026 12:00:00 +0000\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n" +
		"\r\n" +
		"line one\r\nline two\r\n"
	require.Equal(t, want, string(msg))
}

func TestEmail_MessageRejectsInvalidInput(t *testing.T) {
	tests := map[string]func(*Email){
		"header injection": func(e *Email) { e.Subject = "ok\r\nBcc: someone@example.com" },
		"bad sender":       func(e *Email) { e.From = "not an address" },
		"bad recipient":    func(e *Email) { e.To = []string{"nope"} },
		"no recipient":     func(e *Email) { e.To = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			e := testEmail()
			mutate(&e)
			_, err := e.Message()
			require.Error(t, err)
		})
	}
}

func TestSMTPConfig_Validate(t *testing.T) {
	valid := SMTPConfig{Host: "smtp.example.com", Port: 587, Timeout: testTimeout}
	require.NoError(t, valid.Validate())

	for name, cfg := range map[string]SMTPConfig{
		"no host":    {Port: 587, Timeout: testTimeout},
		"no port":    {Host: "smtp.example.com", Timeout: testTimeout},
		"no timeout": {Host: "smtp.example.com", Port: 587},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, cfg.Validate())
		})
	}
}

// fakeSMTPServer accepts one unauthenticated, unencrypted submission and
// returns the envelope and data it received.
type fakeSMTPServer struct {
	listener net.Listener
	received chan fakeSubmission
}

type fakeSubmission struct {
	from string
	to   []string
	data string
}

func startFakeSMTPServer(t *testing.T) *fakeSMTPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { listener.Close() })
	s := &fakeSMTPServer{listener: listener, received: make(chan fakeSubmission, 1)}
	go s.serve()
	return s
}

func (s *fakeSMTPServer) port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

func (s *fakeSMTPServer) serve() {
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	reply := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	var sub fakeSubmission
	reply("220 localhost ESMTP fake")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimRight(line, "\r\n")
		switch upper := strings.ToUpper(cmd); {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			reply("250 localhost")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			sub.from = strings.Trim(cmd[len("MAIL FROM:"):], "<> ")
			reply("250 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			sub.to = append(sub.to, strings.Trim(cmd[len("RCPT TO:"):], "<> "))
			reply("250 OK")
		case upper == "DATA":
			reply("354 go ahead")
			var data strings.Builder
			for {
				dataLine, err := r.ReadString('\n')
				if err != nil || dataLine == ".\r\n" {
					break
				}
				data.WriteString(dataLine)
			}
			sub.data = data.String()
			reply("250 queued")
		case upper == "QUIT":
			reply("221 bye")
			s.received <- sub
			return
		default:
			reply("502 not implemented")
		}
	}
}

func TestSend(t *testing.T) {
	server := startFakeSMTPServer(t)
	cfg := SMTPConfig{Host: "127.0.0.1", Port: server.port(), Timeout: testTimeout}

	require.NoError(t, Send(context.Background(), cfg, testEmail()))

	select {
	case got := <-server.received:
		require.Equal(t, "puckdb@example.com", got.from)
		require.Equal(t, []string{"owner@example.com", "second@example.com"}, got.to)
		require.Contains(t, got.data, "Subject: [puckdb] Yahoo app NOT authorized yet\r\n")
		require.True(t, strings.HasSuffix(got.data, "\r\nline one\r\nline two\r\n"), got.data)
	case <-time.After(testTimeout):
		t.Fatal("fake SMTP server received nothing")
	}
}

func TestSend_ConnectionRefused(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())

	cfg := SMTPConfig{Host: "127.0.0.1", Port: port, Timeout: testTimeout}
	err = Send(context.Background(), cfg, testEmail())
	require.ErrorContains(t, err, "127.0.0.1:"+strconv.Itoa(port))
}
