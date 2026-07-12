import { defineConfig } from "@hey-api/openapi-ts";

export default defineConfig({
  input: "../../contracts/openapi/ascendany-v2.yaml",
  output: {
    path: "src/generated",
    source: true,
  },
});
