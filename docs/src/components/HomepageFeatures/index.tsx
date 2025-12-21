import type { ReactNode } from "react";
import clsx from "clsx";
import Heading from "@theme/Heading";
import styles from "./styles.module.css";

type FeatureItem = {
  title: string;
  description: ReactNode;
};

const FeatureList: FeatureItem[] = [
  {
    title: "Extensible by Design",
    description: (
      <>
        Tokka supports dynamic plugins and middlewares, allowing you to extend
        gateway behavior without recompilation.
      </>
    ),
  },
  {
    title: "Built-in Resilience",
    description: (
      <>
        Per-upstream retry policies, timeouts, status code mapping, and response
        validation out of the box.
      </>
    ),
  },
  {
    title: "Observability First",
    description: (
      <>
        Native metrics support, structured logging, and a built-in dashboard for
        real-time inspection.
      </>
    ),
  },
];

function Feature({ title, description }: FeatureItem) {
  return (
    <div className={clsx("col col--4")}>
      <div className="text--center padding-horiz--md">
        <Heading as="h3">{title}</Heading>
        <p>{description}</p>
      </div>
    </div>
  );
}

export default function HomepageFeatures(): ReactNode {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}
