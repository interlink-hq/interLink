import clsx from 'clsx';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

export default function HomepageVideo(): JSX.Element {
  return (
    <section className={styles.features}>
    <div className="container">
          <div style={{textAlign: 'center'}}>
          <Heading as="h1">
          A world-class HPC at your hand with interLink
        </Heading>
        <iframe src="https://www.youtube.com/embed/-djIQGPvYdI?si=cyYXCkfhDgSZ_VtP" title="YouTube video player"  allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share" allowFullScreen ></iframe>
        </div>
      </div>
      </section>
  );
}
