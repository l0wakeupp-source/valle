package swarm

import (
	"strings"
	"testing"
)

func TestMessageRejectsUnknownSenderAndTarget(t *testing.T) {
	team := NewSwarm("team", "citadel", "goal", TopologyMesh)
	team.AddAgent("rick", "lead")
	team.AddAgent("morty", "worker")

	if err := team.Message(NewMessage("evil-rick", "morty", MsgQuestion, "spoof")); err == nil || !strings.Contains(err.Error(), "sender") {
		t.Fatalf("unknown sender was accepted: %v", err)
	}
	if err := team.Message(NewMessage("rick", "missing", MsgQuestion, "hello")); err == nil || !strings.Contains(err.Error(), "target") {
		t.Fatalf("unknown target was accepted: %v", err)
	}
	if got := team.Agents["morty"].GetMessages(); len(got) != 0 {
		t.Fatalf("rejected messages entered history: %#v", got)
	}
}

func TestMessageFullInboxDoesNotAppendHistory(t *testing.T) {
	team := NewSwarm("team", "citadel", "goal", TopologyMesh)
	team.AddAgent("rick", "lead")
	morty := team.AddAgent("morty", "worker")
	for i := 0; i < cap(morty.Inbox); i++ {
		morty.Inbox <- NewMessage("rick", "morty", MsgResponse, "queued")
	}

	err := team.Message(NewMessage("rick", "morty", MsgQuestion, "overflow"))
	if err == nil || !strings.Contains(err.Error(), "inbox full") {
		t.Fatalf("full inbox was accepted: %v", err)
	}
	if got := morty.GetMessages(); len(got) != 0 {
		t.Fatalf("undelivered message entered history: %#v", got)
	}
}

func TestBroadcastRespectsTopology(t *testing.T) {
	star := NewSwarm("star", "citadel", "goal", TopologyStar)
	star.AddAgent("rick", "primary")
	star.AddAgent("morty", "worker")
	star.AddAgent("summer", "worker")
	star.Primary = "rick"
	if err := star.Message(NewMessage("morty", "*", MsgBroadcast, "unauthorized")); err == nil {
		t.Fatal("non-primary star teammate broadcast to the whole team")
	}
	if err := star.Message(NewMessage("rick", "*", MsgBroadcast, "authorized")); err != nil {
		t.Fatalf("primary star broadcast failed: %v", err)
	}
	for _, name := range []string{"morty", "summer"} {
		if got := star.Agents[name].GetMessages(); len(got) != 1 || got[0].Content != "authorized" {
			t.Fatalf("%s broadcast history = %#v", name, got)
		}
	}

	ring := NewSwarm("ring", "citadel", "goal", TopologyRing)
	ring.AddAgent("rick", "worker")
	ring.AddAgent("morty", "worker")
	if err := ring.Message(NewMessage("rick", "*", MsgBroadcast, "invalid")); err == nil {
		t.Fatal("ring topology accepted a broadcast")
	}
}

func TestRingAndPipelineUseDeclaredAgentOrder(t *testing.T) {
	ring := NewSwarm("ring", "citadel", "goal", TopologyRing)
	for _, name := range []string{"zeta", "alpha", "middle"} {
		ring.AddAgent(name, "worker")
	}
	if err := ring.Message(NewMessage("zeta", "alpha", MsgResponse, "next")); err != nil {
		t.Fatalf("ring ignored declaration order: %v", err)
	}
	if err := ring.Message(NewMessage("middle", "zeta", MsgResponse, "wrap")); err != nil {
		t.Fatalf("ring did not wrap to first declared agent: %v", err)
	}

	pipeline := NewSwarm("pipeline", "citadel", "goal", TopologyPipeline)
	pipeline.AddAgent("first", "worker")
	pipeline.AddAgent("last", "worker")
	if err := pipeline.Message(NewMessage("first", "last", MsgResponse, "forward")); err != nil {
		t.Fatalf("pipeline forward message failed: %v", err)
	}
	if err := pipeline.Message(NewMessage("last", "first", MsgResponse, "wrap")); err == nil {
		t.Fatal("pipeline wrapped its final agent back to the first")
	}
}

func TestSwarmManagerListIsSorted(t *testing.T) {
	manager := NewSwarmManager()
	manager.Add(NewSwarm("zeta", "zeta", "goal", TopologyMesh))
	manager.Add(NewSwarm("alpha", "alpha", "goal", TopologyMesh))
	manager.Add(NewSwarm("middle", "middle", "goal", TopologyMesh))
	got := manager.List()
	want := []string{"alpha", "middle", "zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("manager list = %#v, want %#v", got, want)
		}
	}
}
