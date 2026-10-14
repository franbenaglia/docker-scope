<script lang="ts">
  import type { Summary } from '../lib/api';
  import { fmtTime } from '../lib/format';

  interface Props {
    summary: Summary;
    agentLost: boolean;
  }
  let { summary, agentLost }: Props = $props();
</script>

{#if agentLost}
  <div class="banner warn" role="status">
    Se perdió la conexión en vivo con el agente; reconectando. Los datos son de las
    {fmtTime(summary.generated_at)} y pueden estar desactualizados.
  </div>
{/if}
{#if summary.daemon.status === 'disconnected'}
  <div class="banner bad" role="alert">
    Docker no responde desde las {fmtTime(summary.daemon.since)}.
    {#if summary.stale}
      Se muestra el <strong>último estado conocido</strong> (de las {fmtTime(summary.as_of)}); no significa
      que los contenedores se hayan detenido.
    {:else}
      Todavía no hay datos de contenedores: el estado es <strong>desconocido</strong>.
    {/if}
  </div>
{:else if summary.daemon.status === 'connecting'}
  <div class="banner info" role="status">Conectando con Docker…</div>
{/if}
{#if summary.alerts && summary.alerts.maintenance.length > 0}
  <div class="banner info" role="status">
    Mantenimiento activo:
    {#each summary.alerts.maintenance as m, i (m.id)}
      {i > 0 ? ' · ' : ''}{m.global ? 'global' : m.container} hasta las {fmtTime(m.until)}{m.reason
        ? ` (${m.reason})`
        : ''}
    {/each}
    — se sigue recolectando, pero los avisos se suprimen.
  </div>
{/if}
