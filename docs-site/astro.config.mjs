import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
  site: 'https://bcnelson.github.io',
  base: '/cadence',
  integrations: [
    starlight({
      title: 'cadence',
      description: 'Self-hosted, config-driven monitoring daemon — a Healthchecks.io alternative where YAML is the source of truth.',
      social: {
        github: 'https://github.com/bcnelson/cadence',
      },
      editLink: {
        baseUrl: 'https://github.com/bcnelson/cadence/edit/main/docs-site/',
      },
      sidebar: [
        { label: 'Overview', link: '/' },
        { label: 'Quickstart', link: '/quickstart/' },
        {
          label: 'Install',
          items: [
            { label: 'Binary release', link: '/install/binary/' },
            { label: 'Docker', link: '/install/docker/' },
            { label: 'Nix flake', link: '/install/nix-flake/' },
            { label: 'NixOS module', link: '/install/nixos/' },
          ],
        },
        {
          label: 'Configuration',
          items: [
            { label: 'Overview', link: '/configuration/overview/' },
            { label: 'Checks', link: '/configuration/checks/' },
            { label: 'Channels', link: '/configuration/channels/' },
            { label: 'Ping keys', link: '/configuration/ping-keys/' },
            { label: 'Defaults', link: '/configuration/defaults/' },
            { label: 'Interpolation', link: '/configuration/interpolation/' },
            { label: 'Imports & layering', link: '/configuration/imports/' },
            { label: 'Examples', link: '/configuration/examples/' },
          ],
        },
        {
          label: 'HTTP API',
          items: [
            { label: 'Ping endpoints', link: '/api/ping/' },
            { label: 'Management v3', link: '/api/management-v3/' },
            { label: 'SSE event stream', link: '/api/sse/' },
            { label: 'Health check', link: '/api/healthz/' },
          ],
        },
        {
          label: 'Alerting',
          items: [
            { label: 'Webhooks', link: '/alerting/webhooks/' },
          ],
        },
        {
          label: 'CLI',
          items: [
            { label: 'cadence', link: '/cli/cadence/' },
            { label: 'configtool', link: '/cli/configtool/' },
          ],
        },
        {
          label: 'NixOS module',
          items: [
            { label: 'Overview', link: '/nixos/module/' },
            {
              label: 'Options reference',
              items: [
                { label: 'General', link: '/nixos/options/general/' },
                { label: 'Server', link: '/nixos/options/server/' },
                { label: 'Retention', link: '/nixos/options/retention/' },
                { label: 'Ping keys', link: '/nixos/options/ping-keys/' },
                { label: 'Defaults', link: '/nixos/options/defaults/' },
                { label: 'Channels', link: '/nixos/options/channels/' },
                { label: 'Checks', link: '/nixos/options/checks/' },
              ],
            },
          ],
        },
        { label: 'Dashboard', link: '/dashboard/' },
        { label: 'Contributing', link: '/contributing/' },
      ],
    }),
  ],
});
