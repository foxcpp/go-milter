package milter

import (
	"bytes"
	"net"
	nettextproto "net/textproto"
	"reflect"
	"testing"

	"github.com/emersion/go-message/textproto"
)

type MockMilter struct {
	ConnResp Response
	ConnMod  func(m *Modifier)
	ConnErr  error

	HeloResp Response
	HeloMod  func(m *Modifier)
	HeloErr  error

	MailResp Response
	MailMod  func(m *Modifier)
	MailErr  error

	RcptResp Response
	RcptMod  func(m *Modifier)
	RcptErr  error

	HdrResp Response
	HdrMod  func(m *Modifier)
	HdrErr  error

	HdrsResp Response
	HdrsMod  func(m *Modifier)
	HdrsErr  error

	BodyChunkResp Response
	BodyChunkMod  func(m *Modifier)
	BodyChunkErr  error

	BodyResp Response
	BodyMod  func(m *Modifier)
	BodyErr  error

	// Info collected during calls.
	Host   string
	Family string
	Port   uint16
	Addr   net.IP

	HeloValue string
	From      string
	Rcpt      []string
	Hdr       nettextproto.MIMEHeader

	Chunks [][]byte
}

func (mm *MockMilter) Connect(host string, family string, port uint16, addr net.IP, m *Modifier) (Response, error) {
	if mm.ConnMod != nil {
		mm.ConnMod(m)
	}
	mm.Host = host
	mm.Family = family
	mm.Port = port
	mm.Addr = addr
	return mm.ConnResp, mm.ConnErr
}

func (mm *MockMilter) Helo(name string, m *Modifier) (Response, error) {
	if mm.HeloMod != nil {
		mm.HeloMod(m)
	}
	mm.HeloValue = name
	return mm.HeloResp, mm.HeloErr
}

func (mm *MockMilter) MailFrom(from string, m *Modifier) (Response, error) {
	if mm.MailMod != nil {
		mm.MailMod(m)
	}
	mm.From = from
	return mm.MailResp, mm.MailErr
}

func (mm *MockMilter) RcptTo(rcptTo string, m *Modifier) (Response, error) {
	if mm.RcptMod != nil {
		mm.RcptMod(m)
	}
	mm.Rcpt = append(mm.Rcpt, rcptTo)
	return mm.RcptResp, mm.RcptErr
}

func (mm *MockMilter) Header(name string, value string, m *Modifier) (Response, error) {
	if mm.HdrMod != nil {
		mm.HdrMod(m)
	}
	return mm.HdrResp, mm.HdrErr
}

func (mm *MockMilter) Headers(h nettextproto.MIMEHeader, m *Modifier) (Response, error) {
	if mm.HdrsMod != nil {
		mm.HdrsMod(m)
	}
	mm.Hdr = h
	return mm.HdrsResp, mm.HdrsErr
}

func (mm *MockMilter) BodyChunk(chunk []byte, m *Modifier) (Response, error) {
	if mm.BodyChunkMod != nil {
		mm.BodyChunkMod(m)
	}
	mm.Chunks = append(mm.Chunks, chunk)
	return mm.BodyChunkResp, mm.BodyChunkErr
}

func (mm *MockMilter) Body(m *Modifier) (Response, error) {
	if mm.BodyMod != nil {
		mm.BodyMod(m)
	}
	return mm.BodyResp, mm.BodyErr
}

func TestMilterClient_UsualFlow(t *testing.T) {
	mm := MockMilter{
		ConnResp:      RespContinue,
		HeloResp:      RespContinue,
		MailResp:      RespContinue,
		RcptResp:      RespContinue,
		HdrResp:       RespContinue,
		HdrsResp:      RespContinue,
		BodyChunkResp: RespContinue,
		BodyResp:      SimpleResponse(ActQuarantine),
		BodyMod: func(m *Modifier) {
			m.AddHeader("X-Bad", "very")
			m.ChangeHeader(1, "Subject", "***SPAM***")
		},
	}
	s := Server{
		NewMilter: func() Milter {
			return &mm
		},
		Actions: OptAddHeader | OptChangeHeader,
	}
	defer s.Close()
	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(local)

	cl := NewClient("tcp", local.Addr().String())
	defer cl.Close()
	session, err := cl.Session(OptAddHeader|OptChangeHeader|OptQuarantine, 0)
	if err != nil {
		t.Fatal(err)
	}

	assertAction := func(act *Action, err error, expectCode ActionCode) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if act.Code != expectCode {
			t.Fatal("Unexpectedcode:", act.Code)
		}
	}

	act, err := session.Conn("host", FamilyInet, 25565, "172.0.0.1")
	assertAction(act, err, ActContinue)
	if mm.Host != "host" {
		t.Fatal("Wrong host:", mm.Host)
	}
	if mm.Family != "tcp4" {
		t.Fatal("Wrong family:", mm.Family)
	}
	if mm.Port != 25565 {
		t.Fatal("Wrong port:", mm.Port)
	}
	if mm.Addr.String() != "172.0.0.1" {
		t.Fatal("Wrong IP:", mm.Addr)
	}

	if err := session.Macros(CodeHelo, "tls_version", "very old"); err != nil {
		t.Fatal("Unexpected error", err)
	}

	act, err = session.Helo("helo_host")
	assertAction(act, err, ActContinue)
	if mm.HeloValue != "helo_host" {
		t.Fatal("Wrong helo value:", mm.HeloValue)
	}

	act, err = session.Mail("from@example.org", []string{"A=B"})
	assertAction(act, err, ActContinue)
	if mm.From != "from@example.org" {
		t.Fatal("Wrong MAIL FROM:", mm.From)
	}

	act, err = session.Rcpt("to1@example.org", []string{"A=B"})
	assertAction(act, err, ActContinue)
	act, err = session.Rcpt("to2@example.org", []string{"A=B"})
	assertAction(act, err, ActContinue)
	if !reflect.DeepEqual(mm.Rcpt, []string{"to1@example.org", "to2@example.org"}) {
		t.Fatal("Wrong recipients:", mm.Rcpt)
	}

	hdr := textproto.Header{}
	hdr.Add("From", "from@example.org")
	hdr.Add("To", "to@example.org")
	act, err = session.Header(hdr)
	assertAction(act, err, ActContinue)
	if len(mm.Hdr) != 2 {
		t.Fatal("Unexpected header length:", len(mm.Hdr))
	}
	if val := mm.Hdr.Get("From"); val != "from@example.org" {
		t.Fatal("Wrong From header:", val)
	}
	if val := mm.Hdr.Get("To"); val != "to@example.org" {
		t.Fatal("Wrong To header:", val)
	}

	modifyActs, act, err := session.Body(bytes.NewReader(bytes.Repeat([]byte{'A'}, 128000)))
	assertAction(act, err, ActQuarantine)

	if len(mm.Chunks) != 2 {
		t.Fatal("Wrong amount of body chunks received")
	}
	if len(mm.Chunks[0]) > 65535 {
		t.Fatal("Too big first chunk:", len(mm.Chunks[0]))
	}
	if totalLen := len(mm.Chunks[0]) + len(mm.Chunks[1]); totalLen < 128000 {
		t.Fatal("Some body bytes lost:", totalLen)
	}

	expected := []ModifyAction{
		{
			Code:     ActAddHeader,
			HdrName:  "X-Bad",
			HdrValue: "very",
		},
		{
			Code:     ActChangeHeader,
			HdrIndex: 1,
			HdrName:  "Subject",
			HdrValue: "***SPAM***",
		},
	}

	if !reflect.DeepEqual(modifyActs, expected) {
		t.Fatalf("Wrong modify actions, got %+v", modifyActs)
	}
}