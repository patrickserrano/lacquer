// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightThemeFlexoki from 'starlight-theme-flexoki';

// https://astro.build/config
export default defineConfig({
	site: 'https://patrickserrano.github.io',
	base: '/harness',
	integrations: [
		starlight({
			title: 'harness',
			plugins: [starlightThemeFlexoki()],
			social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/patrickserrano/harness' }],
			sidebar: [
				{
					label: 'Guides',
					items: [
						{ label: 'Getting started', slug: 'guides/getting-started' },
						{ label: 'Agent rules', slug: 'guides/agent-rules' },
					],
				},
				{
					label: 'Reference',
					items: [{ autogenerate: { directory: 'reference' } }],
				},
			],
		}),
	],
});
