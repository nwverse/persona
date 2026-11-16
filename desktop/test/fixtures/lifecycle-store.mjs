export const scenario = { hub: "https://old.example/old/", failWindowSave: false };
export const updates = [];
export class JSONStore {
  static inUserData() { return new JSONStore(); }
  async read() { return { hub: scenario.hub }; }
  async update(patch) { updates.push(patch); }
  async saveWindow() { if (scenario.failWindowSave) throw new Error("synthetic geometry save failure"); }
}
