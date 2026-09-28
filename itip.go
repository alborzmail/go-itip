// Package itip applies RFC 5546 scheduling to go-ical events.
package itip

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// The methods handled (RFC 5546 1.4).
const (
	MethodRequest = "REQUEST"
	MethodCancel  = "CANCEL"
	MethodReply   = "REPLY"
	MethodPublish = "PUBLISH"
)

// ProductID is the PRODID of the calendar objects this package writes.
const ProductID = "-//alborzmail//go-itip//EN"

var (
	ErrOrganizer  = errors.New("itip: not from the event's organizer")
	ErrOutdated   = errors.New("itip: for another revision of the event")
	ErrSuperseded = errors.New("itip: a later answer from this attendee is already applied")
	ErrUninvited  = errors.New("itip: the attendee is not invited")
	ErrInstance   = errors.New("itip: the event holds no such occurrence")
)

// significant are the properties whose change raises SEQUENCE (RFC 5546 2.1.4).
var significant = []string{
	ical.PropDateTimeStart, ical.PropDateTimeEnd, ical.PropDuration, ical.PropDue,
	ical.PropRecurrenceRule, ical.PropRecurrenceDates, ical.PropExceptionDates, ical.PropStatus,
}

// replyStamp keeps on an ATTENDEE the DTSTAMP of the reply its PARTSTAT came from (RFC 5546 2.1.5).
const replyStamp = "X-REPLY-DTSTAMP"

const utcLayout = "20060102T150405Z"

// Parse reads a scheduling message holding at least one event.
func Parse(raw []byte) (*ical.Calendar, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return nil, err
	}
	switch Method(cal) {
	case MethodRequest, MethodCancel, MethodReply, MethodPublish:
	default:
		return nil, fmt.Errorf("itip: method %q not handled", Method(cal))
	}
	if len(cal.Events()) == 0 {
		return nil, errors.New("itip: no event")
	}
	return cal, nil
}

// Method is cal's METHOD, upper-cased.
func Method(cal *ical.Calendar) string {
	method, _ := cal.Props.Text(ical.PropMethod)
	return strings.ToUpper(method)
}

// Sequence is a component's revision; unwritten is zero (RFC 5545 3.8.7.4).
func Sequence(comp *ical.Component) int {
	prop := comp.Props.Get(ical.PropSequence)
	if prop == nil {
		return 0
	}
	n, err := prop.Int()
	if err != nil {
		return 0
	}
	return n
}

func setSequence(comp *ical.Component, n int) {
	prop := ical.NewProp(ical.PropSequence)
	prop.Value = strconv.Itoa(n)
	comp.Props.Set(prop)
}

// Series is the component without RECURRENCE-ID, or nil.
func Series(cal *ical.Calendar) *ical.Component {
	for _, child := range cal.Children {
		if child.Name == ical.CompEvent && child.Props.Get(ical.PropRecurrenceID) == nil {
			return child
		}
	}
	return nil
}

// Attendees are the addresses a component invites.
func Attendees(comp *ical.Component) []string {
	var out []string
	for i := range comp.Props[ical.PropAttendee] {
		out = append(out, Address(&comp.Props[ical.PropAttendee][i]))
	}
	return out
}

// Address is the calendar address a property names, without its mailto: scheme.
func Address(prop *ical.Prop) string {
	value := strings.TrimSpace(prop.Value)
	if strings.HasPrefix(strings.ToLower(value), "mailto:") {
		return value[len("mailto:"):]
	}
	return value
}

func holds(addrs []string, addr string) bool {
	return slices.ContainsFunc(addrs, func(a string) bool { return strings.EqualFold(a, addr) })
}

// Dropped are the attendees before invites and after does not.
func Dropped(before, after *ical.Component) []string {
	if before == nil {
		return nil
	}
	kept := Attendees(after)
	var out []string
	for _, addr := range Attendees(before) {
		if !holds(kept, addr) {
			out = append(out, addr)
		}
	}
	return out
}

// Revise writes after's SEQUENCE: before's, raised by a significant change or a dropped attendee, who is sent a CANCEL.
func Revise(before, after *ical.Component) {
	if before == nil {
		setSequence(after, 0)
		return
	}
	n := Sequence(before)
	if len(Dropped(before, after)) > 0 || slices.ContainsFunc(significant, func(name string) bool {
		return !slices.Equal(propValues(before, name), propValues(after, name))
	}) {
		n++
	}
	setSequence(after, n)
}

func propValues(comp *ical.Component, name string) []string {
	var out []string
	for _, prop := range comp.Props[name] {
		value := prop.Value
		for _, key := range slices.Sorted(maps.Keys(prop.Params)) {
			value += ";" + key + "=" + strings.Join(prop.Params[key], ",")
		}
		out = append(out, value)
	}
	return out
}

// Invited is the first of addresses an event of cal invites, as the event writes it; empty for none.
func Invited(cal *ical.Calendar, addresses []string) string {
	for _, child := range cal.Children {
		if child.Name != ical.CompEvent {
			continue
		}
		for _, addr := range Attendees(child) {
			if holds(addresses, addr) {
				return addr
			}
		}
	}
	return ""
}

// Request is the REQUEST for every event of cal, overrides included.
func Request(cal *ical.Calendar) *ical.Calendar {
	out := message(MethodRequest)
	for _, child := range cal.Children {
		if child.Name == ical.CompEvent {
			event := cloneComponent(child)
			// Alarms are the organizer's own.
			event.Children = nil
			out.Children = append(out.Children, event)
		}
	}
	withZones(out, cal)
	return out
}

// Cancel withdraws cal's event, one revision on: from the attendees named, or from all as CANCELLED when none are.
func Cancel(cal *ical.Calendar, attendees []string, now time.Time) *ical.Calendar {
	out := message(MethodCancel)
	whole := len(attendees) == 0
	for _, src := range cancelled(cal) {
		event := ical.NewComponent(ical.CompEvent)
		copyProps(event, src, ical.PropUID, ical.PropRecurrenceID, ical.PropOrganizer, ical.PropSummary,
			ical.PropLocation, ical.PropDateTimeStart, ical.PropDateTimeEnd, ical.PropDuration)
		event.Props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
		setSequence(event, Sequence(src)+1)
		for _, prop := range src.Props[ical.PropAttendee] {
			if whole || holds(attendees, Address(&prop)) {
				event.Props.Add(ptr(cloneProp(prop)))
			}
		}
		for _, addr := range attendees {
			if !holds(Attendees(src), addr) {
				event.Props.Add(mailto(ical.PropAttendee, addr))
			}
		}
		if whole {
			event.Props.SetText(ical.PropStatus, "CANCELLED")
		}
		out.Children = append(out.Children, event)
	}
	withZones(out, cal)
	return out
}

// The series stands for its overrides (RFC 5546 3.2.5); without one each override is cancelled.
func cancelled(cal *ical.Calendar) []*ical.Component {
	if series := Series(cal); series != nil {
		return []*ical.Component{series}
	}
	var out []*ical.Component
	for _, child := range cal.Children {
		if child.Name == ical.CompEvent {
			out = append(out, child)
		}
	}
	return out
}

// Reply answers every event of request as attendee, at its SEQUENCE and RECURRENCE-ID.
func Reply(request *ical.Calendar, attendee, partstat string, now time.Time) (*ical.Calendar, error) {
	out := message(MethodReply)
	for _, src := range request.Events() {
		if src.Props.Get(ical.PropOrganizer) == nil {
			return nil, errors.New("itip: the request names no organizer")
		}
		event := ical.NewComponent(ical.CompEvent)
		copyProps(event, src.Component, ical.PropUID, ical.PropRecurrenceID, ical.PropSequence, ical.PropOrganizer,
			ical.PropSummary, ical.PropLocation, ical.PropDateTimeStart, ical.PropDateTimeEnd, ical.PropDuration)
		event.Props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
		answer := mailto(ical.PropAttendee, attendee)
		for _, prop := range src.Props[ical.PropAttendee] {
			if strings.EqualFold(Address(&prop), attendee) {
				answer = ptr(cloneProp(prop))
			}
		}
		answer.Params.Set("PARTSTAT", partstat)
		answer.Params.Del("RSVP")
		event.Props.Set(answer)
		out.Children = append(out.Children, event)
	}
	withZones(out, request)
	return out, nil
}

// Apply takes a message into the stored copy of its event, nil for none; the result is nil when nothing remains.
func Apply(stored, msg *ical.Calendar) (*ical.Calendar, error) {
	switch Method(msg) {
	case MethodRequest, MethodPublish:
		return applyRequest(stored, msg)
	case MethodCancel:
		return applyCancel(stored, msg)
	case MethodReply:
		return applyReply(stored, msg)
	}
	return nil, fmt.Errorf("itip: method %q not handled", Method(msg))
}

func applyRequest(stored, msg *ical.Calendar) (*ical.Calendar, error) {
	if stored == nil {
		return kept(msg), nil
	}
	for _, event := range msg.Events() {
		if err := fromOrganizer(stored, event.Component); err != nil {
			return nil, err
		}
		if Sequence(event.Component) < Sequence(instanceOr(stored, event.Component)) {
			return nil, ErrOutdated
		}
	}
	if Series(msg) != nil {
		return kept(msg), nil
	}
	out := cloneCalendar(stored)
	for _, event := range msg.Events() {
		out.Children = slices.DeleteFunc(out.Children, sameInstance(event.Component))
		out.Children = append(out.Children, cloneComponent(event.Component))
	}
	withZones(out, msg)
	return out, nil
}

func applyCancel(stored, msg *ical.Calendar) (*ical.Calendar, error) {
	if stored == nil {
		return nil, nil
	}
	out := cloneCalendar(stored)
	for _, event := range msg.Events() {
		if err := fromOrganizer(out, event.Component); err != nil {
			return nil, err
		}
		if Sequence(event.Component) < Sequence(instanceOr(out, event.Component)) {
			return nil, ErrOutdated
		}
		rid := event.Props.Get(ical.PropRecurrenceID)
		if rid == nil {
			return nil, nil
		}
		out.Children = slices.DeleteFunc(out.Children, sameInstance(event.Component))
		if series := Series(out); series != nil {
			exdate := cloneProp(*rid)
			exdate.Name = ical.PropExceptionDates
			exdate.Params.Del("RANGE")
			series.Props.Add(&exdate)
		}
	}
	if len(out.Events()) == 0 {
		return nil, nil
	}
	return out, nil
}

func applyReply(stored, msg *ical.Calendar) (*ical.Calendar, error) {
	if stored == nil {
		return nil, ErrInstance
	}
	out := cloneCalendar(stored)
	for _, event := range msg.Events() {
		if err := fromOrganizer(out, event.Component); err != nil {
			return nil, err
		}
		target := instance(out, event.Component)
		if target == nil {
			return nil, ErrInstance
		}
		if Sequence(event.Component) != Sequence(target) {
			return nil, ErrOutdated
		}
		answer := event.Props.Get(ical.PropAttendee)
		if answer == nil {
			return nil, ErrUninvited
		}
		stamp, err := event.Props.DateTime(ical.PropDateTimeStamp, time.UTC)
		if err != nil {
			return nil, fmt.Errorf("itip: the reply's DTSTAMP: %v", err)
		}
		list := target.Props[ical.PropAttendee]
		i := slices.IndexFunc(list, func(p ical.Prop) bool { return strings.EqualFold(Address(&p), Address(answer)) })
		if i < 0 {
			return nil, ErrUninvited
		}
		if was, err := time.Parse(utcLayout, list[i].Params.Get(replyStamp)); err == nil && !stamp.After(was) {
			return nil, ErrSuperseded
		}
		list[i].Params.Set("PARTSTAT", answer.Params.Get("PARTSTAT"))
		list[i].Params.Set(replyStamp, stamp.UTC().Format(utcLayout))
	}
	return out, nil
}

func fromOrganizer(stored *ical.Calendar, event *ical.Component) error {
	held := Series(stored)
	if held == nil {
		held = stored.Events()[0].Component
	}
	want, got := held.Props.Get(ical.PropOrganizer), event.Props.Get(ical.PropOrganizer)
	if want == nil || got == nil || !strings.EqualFold(Address(want), Address(got)) {
		return ErrOrganizer
	}
	return nil
}

// instanceKey tells occurrences apart whatever zone their RECURRENCE-ID is written in; empty for the series.
func instanceKey(comp *ical.Component) string {
	prop := comp.Props.Get(ical.PropRecurrenceID)
	if prop == nil {
		return ""
	}
	t, err := prop.DateTime(time.UTC)
	if err != nil {
		return prop.Value
	}
	return t.UTC().Format(utcLayout)
}

func sameInstance(event *ical.Component) func(*ical.Component) bool {
	key := instanceKey(event)
	return func(child *ical.Component) bool {
		return child.Name == ical.CompEvent && instanceKey(child) == key
	}
}

func instance(cal *ical.Calendar, event *ical.Component) *ical.Component {
	if i := slices.IndexFunc(cal.Children, sameInstance(event)); i >= 0 {
		return cal.Children[i]
	}
	return nil
}

// An occurrence with no override of its own is the series'.
func instanceOr(cal *ical.Calendar, event *ical.Component) *ical.Component {
	if found := instance(cal, event); found != nil {
		return found
	}
	if series := Series(cal); series != nil {
		return series
	}
	return ical.NewComponent(ical.CompEvent)
}

func kept(msg *ical.Calendar) *ical.Calendar {
	out := cloneCalendar(msg)
	out.Props.Del(ical.PropMethod)
	out.Props.SetText(ical.PropProductID, ProductID)
	return out
}

func message(method string) *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropProductID, ProductID)
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropMethod, method)
	return cal
}

// withZones gives out the VTIMEZONE src writes for every TZID out names.
func withZones(out, src *ical.Calendar) {
	named := map[string]bool{}
	for _, child := range out.Children {
		for _, props := range child.Props {
			for _, prop := range props {
				if id := prop.Params.Get(ical.PropTimezoneID); id != "" {
					named[id] = true
				}
			}
		}
	}
	for _, child := range src.Children {
		id, _ := child.Props.Text(ical.PropTimezoneID)
		if child.Name == ical.CompTimezone && named[id] && !slices.ContainsFunc(out.Children, func(c *ical.Component) bool {
			held, _ := c.Props.Text(ical.PropTimezoneID)
			return c.Name == ical.CompTimezone && held == id
		}) {
			out.Children = append(out.Children, cloneComponent(child))
		}
	}
}

func copyProps(dst, src *ical.Component, names ...string) {
	for _, name := range names {
		for _, prop := range src.Props[name] {
			dst.Props.Add(ptr(cloneProp(prop)))
		}
	}
}

func mailto(name, addr string) *ical.Prop {
	prop := ical.NewProp(name)
	prop.Value = "mailto:" + addr
	return prop
}

func ptr(prop ical.Prop) *ical.Prop { return &prop }

func cloneCalendar(cal *ical.Calendar) *ical.Calendar {
	return &ical.Calendar{Component: cloneComponent(cal.Component)}
}

func cloneComponent(comp *ical.Component) *ical.Component {
	out := &ical.Component{Name: comp.Name, Props: make(ical.Props, len(comp.Props))}
	for name, props := range comp.Props {
		for _, prop := range props {
			out.Props[name] = append(out.Props[name], cloneProp(prop))
		}
	}
	for _, child := range comp.Children {
		out.Children = append(out.Children, cloneComponent(child))
	}
	return out
}

func cloneProp(prop ical.Prop) ical.Prop {
	params := make(ical.Params, len(prop.Params))
	for key, values := range prop.Params {
		params[key] = slices.Clone(values)
	}
	return ical.Prop{Name: prop.Name, Params: params, Value: prop.Value}
}
