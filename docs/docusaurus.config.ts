import { themes as prismThemes } from "prism-react-renderer";
import type { Config } from "@docusaurus/types";
import type * as Preset from "@docusaurus/preset-classic";

const config: Config = {
  title: "Tokka",
  tagline: "Extensible API Gateway",
  favicon: "img/logo.jpg",

  future: {
    v4: true,
  },

  url: "https://starwalkn.github.io",
  baseUrl: "/tokka/",

  organizationName: "starwalkn",
  projectName: "tokka",

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
          editUrl: "https://github.com/starwalkn/tokka/edit/master/docs/",
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
        alt: "Tokka Logo",
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
          href: "https://github.com/starwalkn/tokka",
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
      //         href: "https://github.com/starwalkn/tokka",
      //       },
      //       {
      //         label: "Issues",
      //         href: "https://github.com/starwalkn/tokka/issues",
      //       },
      //     ],
      //   },
      // ],
      copyright: `Copyright © ${new Date().getFullYear()} Tokka`,
    },

    prism: {
      theme: prismThemes.nightOwlLight,
      darkTheme: prismThemes.nightOwl,
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
