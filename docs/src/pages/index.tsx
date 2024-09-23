import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import HomepageFeatures from '@site/src/components/HomepageFeatures';
import HomepageVideo from '@site/src/components/HomepageVideo';
import HomepageSchema from '@site/src/components/HomepageSchema';
import Heading from '@theme/Heading';
import ThemedImage from '@theme/ThemedImage';
import useBaseUrl from '@docusaurus/useBaseUrl';

import styles from './index.module.css';
import AdoptersFeatures from '../components/AdoptersFeatures';

function HomepageHeader() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <header className={clsx('hero hero--primary', styles.heroBanner)}>
      <div className="container">
        <Heading as="h1" className="hero__title">
              <ThemedImage
        alt="Docusaurus themed image"
            height="300"
        sources={{
          light: useBaseUrl('/img/interlink_logo.png'),
          dark: useBaseUrl('/img/interlink_logo-dark.png'),
        }}
      />
        </Heading>

        <Heading as="h2" className="hero__title">
          {siteConfig.tagline}
        </Heading>
        <div className={styles.buttons}>
          <Link
            className="button button--secondary button--lg"
            to="/docs/intro">
            Try it out! 🚀
          </Link>

        </div>
      <img alt="Stars" src="https://img.shields.io/github/stars/intertwin-eu/interlink" style={{ marginTop: '1rem' }}/>
      <br/>
      <img alt="Slack" src="https://img.shields.io/badge/Join_Slack_Server!-8A2BE2" style={{ marginTop: '1rem' }} onClick={() => window.location.href='https://join.slack.com/t/intertwin/shared_invite/zt-2cs67h9wz-2DFQ6EiSQGS1vlbbbJHctA'}/>
      <HomepageFeatures />
      </div>
    </header>
  );
}

export default function Home(): JSX.Element {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout
      title={`interLink`}
      description="Virtual Kubelets for everyone">
      <HomepageHeader />
      <main>
        <HomepageVideo />

      </main>
    </Layout>
  )
}
