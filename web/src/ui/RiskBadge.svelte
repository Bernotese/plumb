<script lang="ts">
  import type { Risk } from "../lib/types";
  import { riskTone } from "../lib/view";
  import Badge from "./Badge.svelte";

  let { risk }: { risk: Risk } = $props();
</script>

<Badge tone={riskTone[risk]} label={risk} title="Risk class: {risk}" />
