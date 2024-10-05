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
      </>
    ),
  },
  {
    title: 'UPV',
    Svg: require('@site/static/img/cern-logo.svg').default,
    description: (
      <>
      </>
    ),
  },
  {
    title: 'NuNet',
    Svg: require('@site/static/img/cern-logo.svg').default,
    description: (
      <>
      </>
    ),
  },
  {
    title: 'AOB',
    Svg: require('@site/static/img/cern-logo.svg').default,
    description: (
      <>
      </>
    ),
  },
];

function Feature({title, Svg, description}: FeatureItem) {
  return (
    <div className={clsx('col col--4')}>
      <div className="text--left padding-horiz--md">
         <div style={{ backgroundColor: 'darksalmon', display: 'flex', padding: '20px', borderRadius: '10%', width: '220px', height: '220px', justifyContent: 'center', alignItems: 'center', boxShadow: '0 0 2px 1px grey' }}>
           <Svg className={styles.featureSvg} role="img" height="200" />
         </div>
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
         Evaluators and contributors 
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
