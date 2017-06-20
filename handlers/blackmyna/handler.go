package blackmyna

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
	"github.com/pkg/errors"
)

type bmHandler struct {
	handlers.BaseHandler
}

// NewHandler returns a new Blackmyna Handler
func NewHandler() courier.ChannelHandler {
	return &bmHandler{handlers.NewBaseHandler(courier.ChannelType("BM"), "Blackmyna")}
}

func init() {
	courier.RegisterHandler(NewHandler())
}

// Initialize is called by the engine once everything is loaded
func (h *bmHandler) Initialize(s courier.Server) error {
	h.SetServer(s)
	err := s.AddReceiveMsgRoute(h, "GET", "receive", h.ReceiveMessage)
	if err != nil {
		return err
	}

	return s.AddUpdateStatusRoute(h, "GET", "status", h.StatusMessage)
}

// ReceiveMessage is our HTTP handler function for incoming messages
func (h *bmHandler) ReceiveMessage(channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]*courier.Msg, error) {
	// get our params
	bmMsg := &bmMessage{}
	err := handlers.DecodeAndValidateForm(bmMsg, r)
	if err != nil {
		return nil, err
	}

	// create our URN
	urn := courier.NewTelURNForChannel(bmMsg.From, channel)

	// build our msg
	msg := courier.NewIncomingMsg(channel, urn, bmMsg.Text)

	// and finally queue our message
	err = h.Server().WriteMsg(msg)
	if err != nil {
		return nil, err
	}

	return []*courier.Msg{msg}, courier.WriteReceiveSuccess(w, r, msg)
}

type bmMessage struct {
	To   string `validate:"required" name:"to"`
	Text string `validate:"required" name:"text"`
	From string `validate:"required" name:"from"`
}

var bmStatusMapping = map[int]courier.MsgStatus{
	1:  courier.MsgDelivered,
	2:  courier.MsgFailed,
	8:  courier.MsgSent,
	16: courier.MsgFailed,
}

// StatusMessage is our HTTP handler function for status updates
func (h *bmHandler) StatusMessage(channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]*courier.MsgStatusUpdate, error) {
	// get our params
	bmStatus := &bmStatus{}
	err := handlers.DecodeAndValidateForm(bmStatus, r)
	if err != nil {
		return nil, err
	}

	msgStatus, found := bmStatusMapping[bmStatus.Status]
	if !found {
		return nil, fmt.Errorf("unknown status '%d', must be one of 1, 2, 8 or 16", bmStatus.Status)
	}

	// write our status
	status := courier.NewStatusUpdateForExternalID(channel, bmStatus.ID, msgStatus)
	defer status.Release()
	err = h.Server().WriteMsgStatus(status)
	if err != nil {
		return nil, err
	}

	return []*courier.MsgStatusUpdate{status}, courier.WriteStatusSuccess(w, r, status)
}

// SendMsg sends the passed in message, returning any error
func (h *bmHandler) SendMsg(msg *courier.Msg) (*courier.MsgStatusUpdate, error) {
	username := msg.Channel.StringConfigForKey(courier.ConfigUsername, "")
	if username == "" {
		return nil, fmt.Errorf("no username set for BM channel")
	}

	password := msg.Channel.StringConfigForKey(courier.ConfigPassword, "")
	if password == "" {
		return nil, fmt.Errorf("no password set for BM channel")
	}

	apiKey := msg.Channel.StringConfigForKey(courier.ConfigAPIKey, "")
	if apiKey == "" {
		return nil, fmt.Errorf("no API key set for AT channel")
	}

	// build our request
	form := url.Values{
		"address":       []string{msg.URN.Path()},
		"senderaddress": []string{msg.Channel.Address()},
		"message":       []string{msg.Text},
	}

	req, err := http.NewRequest("POST", "http://api.blackmyna.com/2/smsmessaging/outbound", strings.NewReader(form.Encode()))
	req.SetBasicAuth(username, password)
	rr, err := utils.MakeHTTPRequest(req)

	// record our status and log
	status := courier.NewStatusUpdateForID(msg.Channel, msg.ID, courier.MsgErrored)
	status.AddLog(courier.NewChannelLogFromRR(msg.Channel, msg.ID, rr))

	// get our external id
	externalID, _ := jsonparser.GetString([]byte(rr.Body), "[0]", "id")
	if err != nil || externalID == "" {
		return status, errors.Errorf("received error sending message")
	}

	status.Status = courier.MsgWired
	status.ExternalID = externalID

	return status, nil
}

type bmStatus struct {
	ID     string `validate:"required" name:"id"`
	Status int    `validate:"required" name:"status"`
}
