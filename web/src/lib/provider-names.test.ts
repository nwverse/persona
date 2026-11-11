import { describe, expect, it } from "vitest";
import { launcherTitle, namedLauncher, providerIdNote, providerLabel, providerTitle } from "./provider-names";

describe("provider names", () => {
  it("leads with the display name and keeps the ID as the note", () => {
    const l = { name: "team-relay", display_name: "我的中转 站" };
    expect(providerTitle(l)).toBe("我的中转 站");
    expect(providerIdNote(l)).toBe("team-relay");
    expect(providerLabel(l)).toBe("我的中转 站 (team-relay)");
  });

  it("shows the ID alone without a display name, a blank one, or one equal to the ID", () => {
    for (const l of [{ name: "cc_glm" }, { name: "cc_glm", display_name: "  " }, { name: "cc_glm", display_name: "cc_glm" }]) {
      expect(providerTitle(l)).toBe("cc_glm");
      expect(providerIdNote(l)).toBe("");
      expect(providerLabel(l)).toBe("cc_glm");
    }
  });

  it("trims the display name", () => {
    expect(providerTitle({ name: "glm", display_name: " Zhipu GLM " })).toBe("Zhipu GLM");
  });

  it("looks a launcher up by its startup ID", () => {
    const list = [{ name: "glm", display_name: "Zhipu GLM" }, { name: "cc_kimi" }];
    expect(launcherTitle("glm", list)).toBe("Zhipu GLM");
    expect(launcherTitle("cc_kimi", list)).toBe("cc_kimi");
    expect(launcherTitle("gone", list)).toBe("gone");
    expect(launcherTitle("glm", undefined)).toBe("glm");
    expect(namedLauncher("glm", list)).toEqual({ name: "glm", display_name: "Zhipu GLM" });
  });
});
