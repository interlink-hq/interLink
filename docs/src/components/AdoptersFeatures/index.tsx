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
    title: 'INFN',
    Svg: require('@site/static/img/INFN_logo_sito.svg').default,
    description: (
      <>
        ...
      </>
    ),
  },
  {
    title: 'EGI',
    Svg: require('@site/static/img/egi-logo.svg').default,
    description: (
      <>
        ...
      </>
    ),
  },
  {
    title: 'CERN',
    Svg: require('@site/static/img/cern-logo.svg').default,
    description: (
      <>
        INFN
      </>
    ),
  },
  {
    title: 'CERN',
    Svg: require('@site/static/img/cern-logo.svg').default,
    description: (
      <>
        INFN
      </>
    ),
  },
];

function Feature({title, Svg, description}: FeatureItem) {
  return (
    <div className={clsx('col col--4')}>
      <div className="text--center">
        <Svg className={styles.featureSvg} role="img" height="200"  />
      </div>
      <div className="text--center padding-horiz--md">
        <Heading as="h3">{title}</Heading>
        <p>{description}</p>
      </div>
    </div>
  );
}

export default function AdoptersFeatures(): JSX.Element {
  return (
    <section className={styles.features}>
      <div className="container">
          <Heading as="h1">
         Adopters and Contributors 
        </Heading>
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}
