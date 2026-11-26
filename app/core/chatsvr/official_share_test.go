package chatsvr

import (
	"testing"
)

func TestOfficialShareID(t *testing.T) {
	id, err := officialShareID("https://chat.deepseek.com/share/lm3ti95wgxk5iak1ns")
	if err != nil || id != "lm3ti95wgxk5iak1ns" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if _, err := officialShareID("https://example.com/share/x"); err == nil {
		t.Fatal("non-DeepSeek share URL accepted")
	}
}

func TestBuildOfficialShareImport(t *testing.T) {
	messages := []any{
		map[string]any{
			"message_id": 1.0, "parent_id": nil, "role": "USER", "status": "FINISHED",
			"inserted_at": 1795689285.7,
			"fragments":   []any{map[string]any{"type": "REQUEST", "content": "令尊令堂"}},
		},
		map[string]any{
			"message_id": 2.0, "parent_id": 1.0, "role": "ASSISTANT", "status": "FINISHED",
			"inserted_at": 1795689285.7, "thinking_enabled": true,
			"fragments": []any{
				map[string]any{"type": "THINK", "content": "思考", "elapsed_secs": 1.2},
				map[string]any{"type": "RESPONSE", "content": "回答"},
			},
		},
	}
	req, warnings, err := buildOfficialShareImport("Shared Conversation", messages)
	if err != nil {
		t.Fatal(err)
	}
	if req.Title != "令尊令堂" || len(req.Messages) != 2 || len(warnings) != 0 {
		t.Fatalf("req=%+v warnings=%v", req, warnings)
	}
	if len(req.Messages[1].Blocks) != 2 || req.Messages[1].Blocks[1]["text"] != "回答" {
		t.Fatalf("blocks=%+v", req.Messages[1].Blocks)
	}
	if req.Messages[1].ParentID == nil || *req.Messages[1].ParentID != "1" {
		t.Fatalf("parent=%v", req.Messages[1].ParentID)
	}
}
