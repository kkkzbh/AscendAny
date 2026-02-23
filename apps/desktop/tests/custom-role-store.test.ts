import { beforeEach, describe, expect, it } from "vitest";
import { useCustomRoleStore } from "@/stores/customRoleStore";

describe("customRoleStore", () => {
  beforeEach(() => {
    localStorage.clear();
    useCustomRoleStore.setState({ customRoles: [] });
  });

  it("saves and rehydrates custom roles from global local storage", async () => {
    const savedId = useCustomRoleStore.getState().saveCustomRole({
      name: "本地教练",
      avatarUrl: "data:image/png;base64,avatar",
      systemPromptExtra: "你是一名严格但友好的教练。",
    });

    const snapshot = localStorage.getItem("ascendany_custom_roles_global_v1");
    useCustomRoleStore.setState({ customRoles: [] });
    if (snapshot) {
      localStorage.setItem("ascendany_custom_roles_global_v1", snapshot);
    }
    await useCustomRoleStore.persist.rehydrate();

    const role = useCustomRoleStore.getState().customRoles.find((item) => item.id === savedId);
    expect(role?.name).toBe("本地教练");
    expect(role?.systemPromptExtra).toBe("你是一名严格但友好的教练。");
  });

  it("drops invalid persisted custom roles during merge", async () => {
    localStorage.setItem(
      "ascendany_custom_roles_global_v1",
      JSON.stringify({
        state: {
          customRoles: [
            {
              id: "invalid_id",
              name: "坏数据",
              avatarUrl: "",
              systemPromptExtra: "",
            },
          ],
        },
        version: 0,
      }),
    );

    await useCustomRoleStore.persist.rehydrate();
    expect(useCustomRoleStore.getState().customRoles).toHaveLength(0);
  });
});
