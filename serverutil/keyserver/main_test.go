package keyserver

import (
	"reflect"
	"testing"

	"upspin.io/serverutil/signup"
)

func TestMailConfig(t *testing.T) {
	for _, tt := range []struct {
		name     string
		data     string
		exp      *signup.MailConfig
		mailType string
	}{
		{
			name: "key",
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
			mailType: "*mail.logger",
		},
		{
			name:     "key-only",
			data:     "apikey: 123",
			exp:      &signup.MailConfig{},
			mailType: "*sendgrid.sendgrid",
		},
		{
			name:     "empty",
			data:     "",
			exp:      &signup.MailConfig{},
			mailType: "*mail.logger",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mailConfig([]byte(tt.data))
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
		})
	}
}
