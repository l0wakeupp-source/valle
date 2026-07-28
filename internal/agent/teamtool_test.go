package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"rick/internal/swarm"
	"rick/internal/tools"
)

func TestTeamToolUsesRunnerIdentityForMessages(t *testing.T) {
	team := swarm.NewSwarm("team-1", "portal gun", "inspect", swarm.TopologyMesh)
	team.AddAgent("morty", "inspect")
	team.AddAgent("summer", "review")
	tool := TeamTool{Swarm: team}

	input := json.RawMessage(`{"action":"send_message","to":"summer","type":"question","content":"check this"}`)
	result, err := tool.Run(context.Background(), tools.Context{Agent: "morty"}, input)
	if err != nil || result.IsError {
		t.Fatalf("send failed: result=%#v err=%v", result, err)
	}

	messages := team.Agents["summer"].GetMessages()
	if len(messages) != 1 || messages[0].From != "morty" || messages[0].To != "summer" {
		t.Fatalf("runner identity was not preserved: %#v", messages)
	}
}

func TestTeamToolClaimsAndCompletesSharedTask(t *testing.T) {
	team := swarm.NewSwarm("team-1", "portal gun", "inspect", swarm.TopologyMesh)
	team.AddAgent("morty", "inspect")
	if err := team.Tasks.Add(swarm.TaskSpec{ID: "inspect", Subject: "Inspect portal math"}); err != nil {
		t.Fatal(err)
	}
	tool := TeamTool{Swarm: team}
	tc := tools.Context{Agent: "morty"}

	claimed, err := tool.Run(context.Background(), tc, json.RawMessage(`{"action":"claim_task"}`))
	if err != nil || claimed.IsError || !strings.Contains(claimed.Output, "inspect") {
		t.Fatalf("claim failed: result=%#v err=%v", claimed, err)
	}
	completed, err := tool.Run(context.Background(), tc, json.RawMessage(`{"action":"complete_task","task_id":"inspect","result":"safe"}`))
	if err != nil || completed.IsError {
		t.Fatalf("complete failed: result=%#v err=%v", completed, err)
	}

	task, err := team.Tasks.Get("inspect")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != swarm.TaskCompleted || task.Result != "safe" || task.Owner != "morty" {
		t.Fatalf("unexpected task state: %#v", task)
	}
}

func TestTeamToolClaimsSpecificTask(t *testing.T) {
	team := swarm.NewSwarm("team-1", "portal gun", "inspect", swarm.TopologyMesh)
	team.AddAgent("morty", "inspect")
	for _, spec := range []swarm.TaskSpec{{ID: "first", Subject: "First"}, {ID: "second", Subject: "Second"}} {
		if err := team.Tasks.Add(spec); err != nil {
			t.Fatal(err)
		}
	}
	tool := TeamTool{Swarm: team}
	result, err := tool.Run(context.Background(), tools.Context{Agent: "morty"}, json.RawMessage(`{"action":"claim_task","task_id":"second"}`))
	if err != nil || result.IsError || !strings.Contains(result.Output, "second") {
		t.Fatalf("specific claim failed: result=%#v err=%v", result, err)
	}
	first, _ := team.Tasks.Get("first")
	second, _ := team.Tasks.Get("second")
	if first.Status != swarm.TaskPending || second.Status != swarm.TaskInProgress || second.Owner != "morty" {
		t.Fatalf("specific claim changed wrong task: first=%#v second=%#v", first, second)
	}
}

func TestTeamToolRejectsNonMemberIdentity(t *testing.T) {
	team := swarm.NewSwarm("team-1", "portal gun", "inspect", swarm.TopologyMesh)
	team.AddAgent("morty", "inspect")
	tool := TeamTool{Swarm: team}
	result, err := tool.Run(context.Background(), tools.Context{Agent: "evil-rick"}, json.RawMessage(`{"action":"board_put","key":"x","value":"y"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Output, "not a member") {
		t.Fatalf("non-member was accepted: %#v", result)
	}
}

func TestTeamToolReadMessagesConsumesInbox(t *testing.T) {
	team := swarm.NewSwarm("team-1", "portal gun", "inspect", swarm.TopologyMesh)
	team.AddAgent("morty", "inspect")
	summer := team.AddAgent("summer", "review")
	tool := TeamTool{Swarm: team}
	for i := 0; i < cap(summer.Inbox); i++ {
		if err := team.Message(swarm.NewMessage("morty", "summer", swarm.MsgResponse, "queued")); err != nil {
			t.Fatalf("send %d failed: %v", i, err)
		}
	}
	read, err := tool.Run(context.Background(), tools.Context{Agent: "summer"}, json.RawMessage(`{"action":"read_messages"}`))
	if err != nil || read.IsError || !strings.Contains(read.Output, "queued") {
		t.Fatalf("read failed: result=%#v err=%v", read, err)
	}
	if err := team.Message(swarm.NewMessage("morty", "summer", swarm.MsgResponse, "after-read")); err != nil {
		t.Fatalf("inbox stayed full after read: %v", err)
	}
	second, err := tool.Run(context.Background(), tools.Context{Agent: "summer"}, json.RawMessage(`{"action":"read_messages"}`))
	if err != nil || second.IsError || !strings.Contains(second.Output, "after-read") || strings.Contains(second.Output, "queued") {
		t.Fatalf("second read did not return only unread messages: %#v", second)
	}
}
