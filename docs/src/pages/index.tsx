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
import AdopterStoriesSlider from '../components/AdopterStoriesSlider';

function HomepageHeader() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <header className={clsx('hero hero--primary', styles.heroBanner)}>
      <div className="container">
        <div className={styles.heroContent}>
          <div className={styles.logoContainer}>
            <ThemedImage
              alt="interLink logo"
              height="280"
              sources={{
                light: useBaseUrl('/img/interlink_logo.png'),
                dark: useBaseUrl('/img/interlink_logo-dark.png'),
              }}
            />
          </div>
          
          <div className={styles.heroText}>
            <Heading as="h1" className={clsx('hero__title', styles.heroTitle)}>
              interLink
            </Heading>
            <Heading as="h2" className={clsx('hero__subtitle', styles.heroSubtitle)}>
              {siteConfig.tagline}
            </Heading>
            <p className={styles.heroDescription}>
              Bridge your Kubernetes workloads to any compute resource - HPC clusters, 
              batch systems, cloud providers, and more. Maintain the standard Kubernetes 
              API while leveraging the power of heterogeneous computing.
            </p>
            
            <div className={styles.buttons}>
              <Link
                className="button button--secondary button--lg"
                to="/docs/intro">
                Get Started 🚀
              </Link>
            </div>
          </div>
        </div>
        
        <div className={styles.badges}>
          <img 
            alt="GitHub stars" 
            src="https://img.shields.io/github/stars/interlink-hq/interlink?style=for-the-badge&logo=github" 
            className={styles.badge}
            onClick={() => window.open('https://github.com/interlink-hq/interLink', '_blank')}
          />
          <img 
            alt="Go Report Card" 
            src="https://goreportcard.com/badge/github.com/interlink-hq/interlink" 
            className={styles.badge}
            onClick={() => window.open('https://goreportcard.com/report/github.com/interlink-hq/interlink', '_blank')}
          />
          <img 
            alt="Join Slack" 
            src="https://img.shields.io/badge/Join_Slack-4A154B?style=for-the-badge&logo=slack&logoColor=white" 
            className={styles.badge}
            onClick={() => window.open('https://join.slack.com/t/intertwin/shared_invite/zt-2cs67h9wz-2DFQ6EiSQGS1vlbbbJHctA', '_blank')}
          />
        </div>
      </div>
    </header>
  );
}

export default function Home(): JSX.Element {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout
      title={`interLink - Kubernetes to Everything`}
      description="Bridge your Kubernetes workloads to any compute resource - HPC clusters, batch systems, cloud providers, and more.">
      <HomepageHeader />
      <main>
        <AdopterStoriesSlider />
        
        <HomepageFeatures />
        
        <section className={styles.videoSection}>
          <div className="container">
            <Heading as="h2" className={styles.sectionTitle}>
              See interLink in Action
            </Heading>
            <HomepageVideo />
          </div>
        </section>
        
        <section className={styles.cncfSection}>
          <div className="container">
            <Heading as="h2" className={styles.cncfTitle}>
              CNCF Sandbox Project
            </Heading>
            <p className={styles.cncfDescription}>
              interLink is a <a href="https://cncf.io" target="_blank" rel="noopener noreferrer">
                Cloud Native Computing Foundation
              </a> Sandbox project, committed to cloud-native innovation and community collaboration.
            </p>
            <div className={styles.cncfLogoContainer}>
              <img 
                className={styles.cncfLogo} 
                src="https://www.cncf.io/wp-content/uploads/2022/07/cncf-stacked-color-bg.svg" 
                alt="Cloud Native Computing Foundation logo"
              />
            </div>
            <p className={styles.cncfFooter}>
              The Linux Foundation® (TLF) has registered trademarks and uses trademarks. 
              For a list of TLF trademarks, see{' '}
              <a href="https://www.linuxfoundation.org/trademark-usage/" target="_blank" rel="noopener noreferrer">
                Trademark Usage
              </a>.
            </p>
          </div>
        </section>
      </main>
    </Layout>
  )
}
