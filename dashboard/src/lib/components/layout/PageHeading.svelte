<script lang="ts">
  let { title, description, section = 'Workspace' }: { title: string; description: string; section?: string } = $props();
</script>
<div class="page-heading">
  <p class="page-eyebrow">{section}</p>
  <h1>{title}</h1>
  <p>{description}</p>
</div>
