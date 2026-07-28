package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"rick/internal/provider"
	"rick/internal/swarm"
	"rick/internal/tools"
)

func TestSwarmToolWaitsForManagerCompletionAndReturnsResults(t *testing.T) {
	released := make(chan struct{})
	called := make(chan struct{})
	tool := SwarmTool{Manager: func(context.Context, string, string, []SwarmAgentSpec, swarm.Topology) (string, error) {
		close(called)
		<-released
		return "Agent team results:\n[rick] complete\n[morty] complete", nil
	}}
	input, err := json.Marshal(swarmArgs{
		Action: "spawn",
		Name:   "citadel",
		Goal:   "repair portal",
		Agents: []SwarmAgentSpec{{Name: "rick", Role: "inspect"}, {Name: "morty", Role: "verify"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan tools.Result, 1)
	go func() {
		result, _ := tool.Run(context.Background(), tools.Context{}, input)
		done <- result
	}()
	<-called
	select {
	case <-done:
		t.Fatal("swarm tool returned before the team completed")
	default:
	}
	close(released)
	select {
	case result := <-done:
		if result.Title != "agent team completed" || result.Output == "" || result.IsError {
			t.Fatalf("unexpected tool result: %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("swarm tool did not return completed team results")
	}
}

func TestSwarmToolRejectsUnsupportedTopologyBeforeCallingManager(t *testing.T) {
	called := false
	tool := SwarmTool{Manager: func(context.Context, string, string, []SwarmAgentSpec, swarm.Topology) (string, error) {
		called = true
		return "", nil
	}}
	input, err := json.Marshal(swarmArgs{
		Action:   "spawn",
		Goal:     "repair portal",
		Topology: "telepathy",
		Agents:   []SwarmAgentSpec{{Name: "rick", Role: "inspect"}, {Name: "morty", Role: "verify"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Run(context.Background(), tools.Context{}, input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || called {
		t.Fatalf("unsupported topology result=%#v managerCalled=%v", result, called)
	}
}

func TestSwarmToolNormalizesTopologyAndAgentIdentity(t *testing.T) {
	tool := SwarmTool{Manager: func(_ context.Context, _ string, _ string, agents []SwarmAgentSpec, topology swarm.Topology) (string, error) {
		if topology != swarm.TopologyMesh || agents[0].Name != "rick" || agents[0].Role != "inspect" {
			t.Fatalf("manager received unnormalized input: topology=%q agents=%#v", topology, agents)
		}
		return "done", nil
	}}
	input, err := json.Marshal(swarmArgs{
		Action:   " spawn ",
		Goal:     " repair portal ",
		Topology: " MESH ",
		Agents:   []SwarmAgentSpec{{Name: " rick ", Role: " inspect "}, {Name: "morty", Role: "verify"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Run(context.Background(), tools.Context{}, input)
	if err != nil || result.IsError {
		t.Fatalf("normalized spawn failed: result=%#v err=%v", result, err)
	}
}

func TestLastAssistantTextUsesOnlyTerminalAssistantTurn(t *testing.T) {
	messages := []provider.Message{
		provider.AssistantText("preliminary narration"),
		provider.UserText("tool result"),
		provider.AssistantText("terminal teammate result"),
	}
	if got := lastAssistantText(messages); got != "terminal teammate result" {
		t.Fatalf("lastAssistantText = %q", got)
	}
}
