import { themes as prismThemes } from "prism-react-renderer";
import type { Config } from "@docusaurus/types";
import type * as Preset from "@docusaurus/preset-classic";

const config: Config = {
  title: "Kono",
  tagline: "Extensible API Gateway",
  favicon: "img/logo.jpg",

  future: {
    v4: true,
  },

  url: "https://xff16.github.io",
  baseUrl: "/kono/",

  organizationName: "xff16",
  projectName: "kono",

  onBrokenLinks: "throw",

  i18n: {
    defaultLocale: "en",
    locales: ["en"],
  },

  presets: [
    [
      "classic",
      {
        docs: {
          routeBasePath: "/",
          sidebarPath: "./sidebars.ts",
          editUrl: "https://github.com/xff16/kono/edit/master/docs/",
        },
        blog: false,
        theme: {
          customCss: "./src/css/custom.css",
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    image: "img/logo.jpg",

    colorMode: {
      respectPrefersColorScheme: true,
      defaultMode: "light",
    },

    navbar: {
      title: "Home",
      logo: {
        alt: "Kono Logo",
        src: "img/logo.jpg",
      },
      items: [
        {
          type: "docSidebar",
          sidebarId: "tutorialSidebar",
          label: "Docs",
          position: "left",
        },
        {
          href: "https://github.com/xff16/kono",
          label: "GitHub",
          position: "right",
        },
      ],
    },

    footer: {
      // style: "dark",
      // links: [
      //   {
      //     title: "Docs",
      //     items: [
      //       {
      //         label: "Getting Started",
      //         to: "/",
      //       },
      //     ],
      //   },
      //   {
      //     title: "Project",
      //     items: [
      //       {
      //         label: "GitHub",
      //         href: "https://github.com/xff16/kono",
      //       },
      //       {
      //         label: "Issues",
      //         href: "https://github.com/xff16/kono/issues",
      //       },
      //     ],
      //   },
      // ],
      copyright: `Copyright © ${new Date().getFullYear()} Kono`,
    },

    prism: {
      theme: prismThemes.nightOwlLight,
      darkTheme: prismThemes.nightOwl,
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
