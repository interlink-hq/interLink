import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';
import type * as Redocusaurus from 'redocusaurus';

const config: Config = {
  title: 'interLink',
  tagline: 'Your Virtual Kubelet ecosystem!',
  favicon: 'img/favicon.ico',

  // Set the production url of your site here
  url: 'https://interlink-project.dev',
  // Set the /<baseUrl>/ pathname under which your site is served
  // For GitHub pages deployment, it is often '/<projectName>/'
  baseUrl: '/',

  // GitHub pages deployment config.
  // If you aren't using GitHub pages, you don't need these.
  organizationName: 'interlink-hq', // Usually your GitHub org/user name.
  projectName: 'interLink', // Usually your repo name.

  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'warn',

  // Even if you don't use internationalization, you can use this field to set
  // useful metadata like html lang. For example, if your site is Chinese, you
  // may want to replace "en" with "zh-Hans".
  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          // Please change this to your repo.
          // Remove this to remove the "edit this page" links.
          editUrl:
            'https://github.com/interlink-hq/interLink',
        },
        blog: false, 
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
    [
     'redocusaurus',
      {
        // Plugin Options for loading OpenAPI files
        specs: [
          // Pass it a path to a local OpenAPI YAML file
          {
            // Redocusaurus will automatically bundle your spec into a single file during the build
            id: 'plugin-api',
            spec: 'openapi/plugin-openapi.json',
            route: '/plugin-openapi/',
          },
          {
            // Redocusaurus will automatically bundle your spec into a single file during the build
            id: 'interlink-api',
            spec: 'openapi/interlink-openapi.json',
            route: '/interlink-openapi/',
          },
        ],
        // Theme Options for modifying how redoc renders them
        theme: {
          // Change with your site colors
          primaryColor: '#1890ff',
        },
      },
    ], 

  ],

  themeConfig: {
      announcementBar: {
      id: 'support_us',
      content:
        'We are onboarding for our contribution to CNCF Sandbox! Please let us know for any broken or missing information as we move to the new home.',
      backgroundColor: '#fafbfc',
      textColor: '#091E42',
      isCloseable: false,
    },

    // Replace with your project's social card
    image: 'img/img/interlink_logo.png',
    navbar: {
      title: 'Home',
      logo: {
        alt: 'interLink Logo',
        src: 'img/interlink_logo.png',
      },
      items: [
        {
          type: 'docsVersionDropdown',
          position: 'left',
          dropdownActiveClassDisabled: true,
        },
        {
          type: 'docSidebar',
          sidebarId: 'tutorialSidebar',
          position: 'left',
          label: 'Docs',
        },
        {
          href: 'https://github.com/interlink-hq/interLink',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Docs',
          items: [
            {
              label: 'Docs',
              to: '/docs/intro',
            },
          ],
        },
        {
          title: 'Community',
          items: [
            {
              label: 'interTwin project Slack',
              href: 'https://join.slack.com/t/intertwin/shared_invite/zt-2cs67h9wz-2DFQ6EiSQGS1vlbbbJHctA',
            }
          ],
        },
        {
          title: 'More',
          items: [
            {
              label: 'GitHub',
              href: 'https://github.com/interlink-hq/interLink',
            },
          ],
        },
      ],
      copyright: `Originally created by INFN - Copyright © interLink a Series of LF Projects, LLC.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
