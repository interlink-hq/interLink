import clsx from 'clsx';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

type FeatureItem = {
  title: string;
  Svg: React.ComponentType<React.ComponentProps<'svg'>>;
  description: JSX.Element;
};

const FeatureList: FeatureItem[] = [
  {
    title: 'Seamless end-user experience',
    Svg: require('@site/static/img/home-1.svg').default,
    description: (
      <>
        Keep using all your data science frameworks just like you are
        interacting with a physical Kubernetes cluster.
      </>
    ),
  },
  {
    title: 'Customize only What Matters',
    Svg: require('@site/static/img/home-2.svg').default,
    description: (
      <>
        {/*Docusaurus lets you focus on your docs, and we&apos;ll do the chores. Go
        ahead and move your docs into the <code>docs</code> directory.*/}
        Forget about complex API&apos;s and kubelet internals;
        focus on a simple REST interface for managing container lifecycle as you wish.
      </>
    ),
  },
  {
    title: 'Integrate your resources quickly',
    Svg: require('@site/static/img/home-3.svg').default,
    description: (
      <>
        Integrate resources hosted on remote batch systems or FaaS instances in matter
        of minutes thanks to interLink plugin-based architecture.
      </>
    ),
  },
];

function Feature({title, Svg, description}: FeatureItem) {
  return (
    <div className={clsx('col col--4')}>
      <div className="text--center">
        <Svg className={styles.featureSvg} role="img" />
      </div>
      <div className="text--center padding-horiz--md">
        <Heading as="h3">{title}</Heading>
        <p>{description}</p>
      </div>
    </div>
  );
}

export default function HomepageFeatures(): JSX.Element {
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
