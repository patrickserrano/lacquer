// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightThemeFlexoki from 'starlight-theme-flexoki';

// https://astro.build/config
export default defineConfig({
	site: 'https://patrickserrano.github.io',
	base: '/lacquer',
	integrations: [
		starlight({
			title: 'lacquer',
			plugins: [starlightThemeFlexoki({ accentColor: 'purple' })],
			social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/patrickserrano/lacquer' }],
			sidebar: [
				{
					label: 'Guides',
					items: [
						{ label: 'Getting started', slug: 'guides/getting-started' },
						{ label: 'Agent rules', slug: 'guides/agent-rules' },
						{ label: 'iOS rules', slug: 'guides/ios-rules' },
						{ label: 'Web rules', slug: 'guides/web-rules' },
						{ label: 'Supabase rules', slug: 'guides/supabase-rules' },
					],
				},
				{
					label: 'Reference',
					items: [{ autogenerate: { directory: 'reference' } }],
				},
				{
					label: 'Skills',
					items: [
						{ label: 'core', items: [{ autogenerate: { directory: 'skills/core' } }] },
						{ label: 'ios', items: [{ autogenerate: { directory: 'skills/ios' } }] },
						{ label: 'supabase', items: [{ autogenerate: { directory: 'skills/supabase' } }] },
					],
				},
			],
		}),
	],
});
