package keyserver

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"upspin.io/log"
	"upspin.io/serverutil/signup"
)

// mockLogger implements a log.ExternalLogger which stores the last logged
// message.
type mockLogger struct {
	lastMsg string
}

func (ml *mockLogger) Log(_ log.Level, txt string) {
	ml.lastMsg = txt
}

func (mockLogger) Flush() {}

func (ml mockLogger) LastMessage() string {
	return ml.lastMsg
}

var _ log.ExternalLogger = (*mockLogger)(nil)

func TestMailConfig(t *testing.T) {
	ml := new(mockLogger)
	log.Register(ml)
	log.SetOutput(nil)
	defer func() {
		log.SetOutput(os.Stderr)
	}()
	for _, tt := range []struct {
		name     string
		data     string
		exp      *signup.MailConfig
		msg      string
		mailType string
	}{
		{
			name: "all",
			data: `
apikey: 123
project: test
notify: send@not.if
from: from@addr.com`,
			exp: &signup.MailConfig{
				Project: "test",
				Notify:  "send@not.if",
				From:    "from@addr.com",
			},
			mailType: "*sendgrid.sendgrid",
		},
		{
			name: "no-project",
			data: `
apikey: 123
notify: send@not.if
from: from@addr.com`,
			exp: &signup.MailConfig{
				Notify: "send@not.if",
				From:   "from@addr.com",
			},
			msg:      "project name not supplied",
			mailType: "*sendgrid.sendgrid",
		},
		{
			name: "no-key",
			data: `
project: test
notify: send@not.if
from: from@addr.com`,
			exp: &signup.MailConfig{
				Project: "test",
				Notify:  "send@not.if",
				From:    "from@addr.com",
			},
			msg:      "WARNING: apikey is missing",
			mailType: "*mail.logger",
		},
		{
			name:     "key-only",
			data:     "apikey: 123",
			exp:      &signup.MailConfig{},
			msg:      "project name not supplied",
			mailType: "*sendgrid.sendgrid",
		},
		{
			name: "missing-from",
			data: `
apikey: 123
notify: asd@asd.com`,
			exp:      &signup.MailConfig{},
			msg:      "missing from address",
			mailType: "*sendgrid.sendgrid",
		},
		{
			name:     "empty",
			data:     "",
			exp:      &signup.MailConfig{},
			msg:      "WARNING: apikey is missing",
			mailType: "*mail.logger",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMailConfig([]byte(tt.data))
			if err != nil {
				t.Fatal(err)
			}
			if got := reflect.TypeOf(got.Mail).String(); got != tt.mailType {
				t.Fatalf("expected mail to be of type %s but got %s", tt.mailType, got)
			}
			tt.exp.Mail = got.Mail // copy so that we can compare
			if !reflect.DeepEqual(tt.exp, got) {
				t.Fatalf("expected %#v, got %#v", tt.exp, got)
			}
			if want := tt.msg; want != "" {
				if got := ml.LastMessage(); !strings.Contains(got, want) {
					t.Fatalf("expected message '%s', got '%s'", want, got)
				}
			}
		})
	}
}
