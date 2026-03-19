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
      </>
    ),
  },
  {
    title: 'EGI',
    Svg: require('@site/static/img/egi-logo.svg').default,
    description: (
      <>
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
    title: 'Universitat Politècnica de València',
    Svg: require('@site/static/img/logo-upv.svg').default,
    description: (
      <>
      </>
    ),
  },
  {
    title: 'CNES',
    Svg: require('@site/static/img/logo-cnes.svg').default,
    description: (
      <>
      </>
    ),
  },
  {
    title: 'IJS',
    Svg: require('@site/static/img/logo-ijs.svg').default,
    description: (
      <>
      </>
    ),
  },
  {
    title: 'IZUM',
    Svg: require('@site/static/img/logo-izum.svg').default,
    description: (
      <>
      </>
    ),
  },
  {
    title: 'JSC',
    Svg: require('@site/static/img/logo-jsc.svg').default,
    description: (
      <>
      </>
    ),
  },
  {
    title: 'NuNet',
    Svg: require('@site/static/img/logo-nunet.svg').default,
    description: (
      <>
      </>
    ),
  },
  {
    title: 'HelixML',
    Svg: require('@site/static/img/logo-helix.svg').default,
    description: (
      <>
      </>
    ),
  },
];

function Feature({title, Svg, description}: FeatureItem) {
  return (
    <div className={clsx('col col--3')}>
         <div style={{ backgroundColor: 'darkgrey', display: 'flex', padding: '20px', borderRadius: '10%', width: '220px', height: '220px', justifyContent: 'center', alignItems: 'center', boxShadow: '0 0 2px 1px grey' }}>
           <Svg className={styles.featureSvg} role="img" height="100" width="100" />
         </div>
        <p></p>
   </div>
  );
}

export default function AdoptersFeatures(): JSX.Element {
  return (
    <section className={styles.features}>
      <div className="container">
          <Heading as="h1">
         Evaluators and contributors 
        <p>Find out more in the ADOPTERS.md document! </p>
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
