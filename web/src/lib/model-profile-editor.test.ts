import { beforeAll, describe, expect, it } from "vitest";
import { initI18n } from "../i18n";
import {
  captureProfileInput,
  parseProfileChange,
  profileApplyDrift,
  type ModelProfile,
} from "./model-profiles";
import {
  draftToProfileInput,
  isProfileDraftDirty,
  profileDraftProblems,
  profileToDraft,
} from "./model-profile-editor";

beforeAll(async () => {
  await initI18n("en");
});

const baseProfile: ModelProfile = {
  id: "economy",
  name: "Economy",
  description: "Everyday models",
  purposes: {
    assistant: { provider: "launcher.gw", model: "sonnet-4", effort: "medium", permission_mode: "acceptEdits" },
    kb: {},
  },
  launchers: {
    gw: { model: "sonnet-4", small_model: "", effort: "low" },
  },
  tiers: {
    strong: { launcher: "gw", model: "opus-4", effort: "high", permission_mode: "plan", speed: "fast", description: "Hard work" },
  },
  changes: [],
};

describe("model-profile-editor", () => {
  it("round-trips a profile through draft and save input without emitting config_dir or secrets", () => {
    const draft = profileToDraft(baseProfile, ["assistant", "agent.new", "kb", "inbox.triage", "agent.delegate", "translate"]);
    expect(isProfileDraftDirty(draft, profileToDraft(baseProfile))).toBe(false);
    expect(profileDraftProblems(draft, { gw: "launcher" })).toEqual([]);

    const input = draftToProfileInput(draft, 5);
    expect(input).toEqual({
      revision: 5,
      name: "Economy",
      description: "Everyday models",
      purposes: {
        assistant: { provider: "launcher.gw", model: "sonnet-4", effort: "medium", permission_mode: "acceptEdits" },
        kb: {},
      },
      launchers: {
        gw: { model: "sonnet-4", small_model: "", effort: "low" },
      },
      tiers: {
        strong: { launcher: "gw", model: "opus-4", effort: "high", permission_mode: "plan", speed: "fast", description: "Hard work" },
      },
    });
    expect(JSON.stringify(input)).not.toContain("config_dir");
    expect(JSON.stringify(input)).not.toContain("api_key");
  });

  it("omits unchecked purposes and empty launcher entries", () => {
    const draft = profileToDraft(baseProfile);
    draft.purposes.kb.included = false;
    draft.launchers.gw = {};
    const input = draftToProfileInput(draft, 2);
    expect(input.purposes).toEqual({
      assistant: { provider: "launcher.gw", model: "sonnet-4", effort: "medium", permission_mode: "acceptEdits" },
    });
    expect(input.launchers).toBeUndefined();
    expect(isProfileDraftDirty(draft, profileToDraft(baseProfile))).toBe(true);
  });

  it("validates profile name, launcher API restrictions, and tier constraints", () => {
    const draft = profileToDraft(baseProfile);
    draft.name = "   ";
    draft.launchers.api_only = { model: "m-1", small_model: "m-2" };
    draft.tiers.push({
      key: "bad-1",
      name: "Invalid Name",
      launcher: "",
      model: "bad model id!",
      effort: "extreme",
      permission_mode: "bogusMode",
      speed: "warp",
      description: "line1\nline2",
    });
    const problems = profileDraftProblems(draft, { gw: "launcher", api_only: "api" });
    expect(problems.length).toBeGreaterThanOrEqual(7);
    expect(problems.some((p) => p.includes("Profile name"))).toBe(true);
    expect(problems.some((p) => p.includes("api_only"))).toBe(true);
    expect(problems.some((p) => p.includes("Invalid Name"))).toBe(true);
    expect(problems.some((p) => p.includes("bad model id!"))).toBe(true);
    expect(problems.some((p) => p.includes("permission_mode"))).toBe(true);
  });

  it("detects duplicate tier names and preserves untouched legacy tier model IDs", () => {
    const legacy: ModelProfile = {
      id: "legacy",
      name: "Legacy",
      tiers: {
        old: { launcher: "claude", model: "bad!legacy" },
      },
      changes: [],
    };
    const draft = profileToDraft(legacy);
    // Untouched legacy model ID does not block editing unrelated fields (mirrors server/internal/assistant/model_profiles_edit.go).
    expect(profileDraftProblems(draft)).toEqual([]);

    // Adding a duplicate tier name is flagged.
    draft.tiers.push({
      key: "dup",
      name: "old",
      launcher: "claude",
      model: "valid-model",
      effort: "",
      permission_mode: "",
      speed: "",
      description: "",
    });
    expect(profileDraftProblems(draft).some((p) => p.includes("Duplicate"))).toBe(true);
  });

  it("omits empty optional fields and default standard speed for cross-version hub compatibility", () => {
    const profile: ModelProfile = {
      id: "compat",
      name: "Compat",
      purposes: {
        assistant: { provider: "launcher.claude", model: "sonnet-4" },
      },
      tiers: {
        standard: { launcher: "claude", model: "sonnet-4", speed: "standard" },
        quick: { launcher: "claude", speed: "fast", permission_mode: "plan" },
      },
      changes: [],
    };
    const draft = profileToDraft(profile);
    const input = draftToProfileInput(draft, 4);
    expect(input.purposes?.assistant).toEqual({ provider: "launcher.claude", model: "sonnet-4" });
    expect("permission_mode" in (input.purposes?.assistant ?? {})).toBe(false);
    expect(input.tiers?.standard).toEqual({ launcher: "claude", model: "sonnet-4" });
    expect("speed" in (input.tiers?.standard ?? {})).toBe(false);
    expect("permission_mode" in (input.tiers?.standard ?? {})).toBe(false);
    expect(input.tiers?.quick).toEqual({ launcher: "claude", speed: "fast", permission_mode: "plan" });
    expect("capture" in input).toBe(false);
  });

  it("builds minimal capture: true payloads preserving name and description without tiers", () => {
    expect(captureProfileInput({ name: "Performance", description: "Fast models" }, 7)).toEqual({
      revision: 7,
      name: "Performance",
      description: "Fast models",
      capture: true,
    });
    const noDesc = captureProfileInput({ name: "Economy", description: "" }, 2);
    expect(noDesc).toEqual({ revision: 2, name: "Economy", capture: true });
    expect("description" in noDesc).toBe(false);
    expect("tiers" in noDesc).toBe(false);
    expect("purposes" in noDesc).toBe(false);
    expect("launchers" in noDesc).toBe(false);
  });

  it("parses dotted category IDs and dotted launcher field changes unambiguously", () => {
    expect(parseProfileChange("assistant")).toEqual({ kind: "purpose", id: "assistant" });
    expect(parseProfileChange("agent.new")).toEqual({ kind: "purpose", id: "agent.new" });
    expect(parseProfileChange("inbox.triage")).toEqual({ kind: "purpose", id: "inbox.triage" });
    expect(parseProfileChange("agent.delegate")).toEqual({ kind: "purpose", id: "agent.delegate" });
    expect(parseProfileChange("gw.small_model")).toEqual({ kind: "launcher", launcher: "gw", field: "small_model" });
    expect(parseProfileChange("relay.example.com.effort")).toEqual({ kind: "launcher", launcher: "relay.example.com", field: "effort" });
    expect(parseProfileChange("custom.unknown_field")).toEqual({ kind: "other", raw: "custom.unknown_field" });
  });

  it("computes apply drift and active-profile capture availability accurately", () => {
    expect(profileApplyDrift({ id: "economy", changes: [] }, { id: "performance", changes: [] })).toEqual({
      targetChanges: [],
      activeDrift: [],
      hasDrift: false,
      hasActiveDrift: false,
      canCaptureActiveFirst: true,
    });
    expect(profileApplyDrift({ id: "economy", changes: ["assistant"] }, { id: "performance", changes: ["kb"] })).toEqual({
      targetChanges: ["assistant"],
      activeDrift: ["kb"],
      hasDrift: true,
      hasActiveDrift: true,
      canCaptureActiveFirst: true,
    });
    expect(profileApplyDrift({ id: "economy", changes: [] }, { id: "performance", changes: ["translate"] })).toEqual({
      targetChanges: [],
      activeDrift: ["translate"],
      hasDrift: true,
      hasActiveDrift: true,
      canCaptureActiveFirst: true,
    });
    expect(profileApplyDrift({ id: "performance", changes: ["assistant"] }, { id: "performance", changes: ["assistant"] })).toEqual({
      targetChanges: ["assistant"],
      activeDrift: [],
      hasDrift: true,
      hasActiveDrift: false,
      canCaptureActiveFirst: false,
    });
    expect(profileApplyDrift({ id: "economy", changes: ["assistant"] }, undefined)).toEqual({
      targetChanges: ["assistant"],
      activeDrift: [],
      hasDrift: true,
      hasActiveDrift: false,
      canCaptureActiveFirst: false,
    });
  });
});
