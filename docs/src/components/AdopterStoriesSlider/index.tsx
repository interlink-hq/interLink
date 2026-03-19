import React, { useState, useEffect } from 'react';
import clsx from 'clsx';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

type AdopterStory = {
  name: string;
  category: string;
  logo: string;
  project?: string;
  title: string;
  description: string;
  link?: string;
};

const AdopterStories: AdopterStory[] = [
  {
    name: 'INFN',
    category: 'Scientific Communities',
    logo: require('@site/static/img/INFN_logo_sito.svg').default,
    project: 'Heterogeneous Resource Integration',
    title: 'AI/ML Pipelines on HPC/HTC Centers',
    description: 'INFN leverages interLink to enable seamless provisioning of heterogeneous resources for Kubernetes-based workloads, making transparent the exploitation of HPC centers for AI/ML pipelines without customization on the user end.',
  },
  {
    name: 'CERN',
    category: 'Scientific Communities', 
    logo: require('@site/static/img/cern-logo.svg').default,
    project: 'interTwin',
    title: 'ML/AI Workloads on HPC with CI/CD Integration',
    description: 'CERN uses interLink to offload ML/AI workloads to HPC for physics and climate research, enabling distributed training and inference while automatically connecting container CI/CD pipelines with HPC for integration testing.',
  },
  {
    name: 'EGI Foundation',
    category: 'Scientific Communities',
    logo: require('@site/static/img/egi-logo.svg').default,
    title: 'Cloud Container Compute Service Integration',
    description: 'EGI integrates interLink to provide seamless integration of HPC centers with their Cloud Container compute service, building the foundation for new EC projects RI-SCALE and EOSC Data Commons.',
  },
  {
    name: 'Universitat Politècnica de València',
    category: 'Scientific Communities',
    logo: require('@site/static/img/logo-upv.svg').default,
    project: 'OSCAR Integration',
    title: 'Serverless Data-Processing on HPC',
    description: 'UPV integrated interLink capabilities into OSCAR for event-driven serverless computing, enabling efficient offloading of data-processing applications to HPC clusters with seamless resource provisioning.',
  },
  {
    name: 'JSC',
    category: 'HPC Supercomputing Centers',
    logo: require('@site/static/img/logo-jsc.svg').default,
    project: 'JSC Cloud + JUWELS',
    title: 'UNICORE Middleware Integration',
    description: 'JSC provides seamlessly integrated cloud and HPC resources through UNICORE middleware, using interLink to transform pod creation requests into HPC jobs for the powerful JUWELS system.',
  },
  {
    name: 'IJS & IZUM',
    category: 'HPC Supercomputing Centers',
    logo: require('@site/static/img/logo-ijs.svg').default,
    project: 'EuroHPC Vega',
    title: 'First Operational EuroHPC System',
    description: 'EuroHPC Vega serves as the first operational system under the EuroHPC initiative, providing critical infrastructure and support for interLink development and utilization.',
  },
  {
    name: 'CNES',
    category: 'HPC Supercomputing Centers',
    logo: require('@site/static/img/logo-cnes.svg').default,
    project: 'LISA DDPC',
    title: 'Hybrid Kubernetes/Slurm Execution',
    description: 'CNES prototypes hybrid execution of LISA (Laser Interferometer Space Antenna) pipelines using interLink to seamlessly distribute workloads between Kubernetes and Slurm resources.',
  },
];

export default function AdopterStoriesSlider(): JSX.Element {
  const [currentIndex, setCurrentIndex] = useState(0);
  const [isAutoPlay, setIsAutoPlay] = useState(true);

  useEffect(() => {
    if (!isAutoPlay) return;
    
    const interval = setInterval(() => {
      setCurrentIndex((prevIndex) => 
        prevIndex === AdopterStories.length - 1 ? 0 : prevIndex + 1
      );
    }, 5000);

    return () => clearInterval(interval);
  }, [isAutoPlay]);

  const goToSlide = (index: number) => {
    setCurrentIndex(index);
    setIsAutoPlay(false);
    setTimeout(() => setIsAutoPlay(true), 10000); // Resume auto-play after 10 seconds
  };

  const nextSlide = () => {
    setCurrentIndex((prevIndex) => 
      prevIndex === AdopterStories.length - 1 ? 0 : prevIndex + 1
    );
    setIsAutoPlay(false);
    setTimeout(() => setIsAutoPlay(true), 10000);
  };

  const prevSlide = () => {
    setCurrentIndex((prevIndex) => 
      prevIndex === 0 ? AdopterStories.length - 1 : prevIndex - 1
    );
    setIsAutoPlay(false);
    setTimeout(() => setIsAutoPlay(true), 10000);
  };

  const currentStory = AdopterStories[currentIndex];
  const Logo = currentStory.logo;

  return (
    <section className={styles.sliderSection}>
      <div className="container">
        <Heading as="h2" className={styles.sectionTitle}>
          Real-World Use Cases
        </Heading>
        <p className={styles.sectionSubtitle}>
          See how organizations worldwide use interLink to bridge Kubernetes and HPC
        </p>
        
        <div className={styles.sliderContainer}>
          <div className={styles.sliderContent}>
            <div className={styles.storyCard}>
              <div className={styles.storyHeader}>
                <div className={styles.logoContainer}>
                  <Logo className={styles.storyLogo} />
                </div>
                <div className={styles.storyMeta}>
                  <h3 className={styles.storyName}>{currentStory.name}</h3>
                  <span className={styles.storyCategory}>{currentStory.category}</span>
                  {currentStory.project && (
                    <span className={styles.storyProject}>Project: {currentStory.project}</span>
                  )}
                </div>
              </div>
              
              <div className={styles.storyBody}>
                <h4 className={styles.storyTitle}>{currentStory.title}</h4>
                <p className={styles.storyDescription}>{currentStory.description}</p>
              </div>
            </div>
          </div>

          <button 
            className={clsx(styles.sliderButton, styles.prevButton)} 
            onClick={prevSlide}
            aria-label="Previous story"
          >
            ‹
          </button>
          <button 
            className={clsx(styles.sliderButton, styles.nextButton)} 
            onClick={nextSlide}
            aria-label="Next story"
          >
            ›
          </button>
        </div>

        <div className={styles.sliderDots}>
          {AdopterStories.map((_, index) => (
            <button
              key={index}
              className={clsx(styles.dot, index === currentIndex && styles.activeDot)}
              onClick={() => goToSlide(index)}
              aria-label={`Go to story ${index + 1}`}
            />
          ))}
        </div>

        <div className={styles.sliderFooter}>
          <p>
            Learn more about all our adopters in the{' '}
            <a href="https://github.com/interlink-hq/interLink/blob/main/ADOPTERS.md" target="_blank" rel="noopener noreferrer">
              ADOPTERS.md
            </a>{' '}
            document
          </p>
        </div>
      </div>
    </section>
  );
}