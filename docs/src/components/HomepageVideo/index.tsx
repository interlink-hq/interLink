import clsx from 'clsx';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

export default function HomepageVideo(): JSX.Element {
  return (
    <section className={styles.features}>
    <div className="container">
                  <Heading as="h1">
        Video material 
        </Heading>

          <div style={{textAlign: 'left'}}>
          <Heading as="h2">
          Interlink overview at Kubecon colocated CloudNative AI Day
        </Heading>
        <iframe src="https://www.youtube.com/embed/M3uLQiekqo8?si=-xv8bUNNJKJmMt_V" title="YouTube video player"  allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share" allowFullScreen ></iframe>
        </div>
          <div style={{textAlign: 'left'}}>
          <Heading as="h2">
          SLURM at a EuroHPC is at your hand with interLink
        </Heading>
        <iframe src="https://www.youtube.com/embed/-djIQGPvYdI?si=cyYXCkfhDgSZ_VtP" title="YouTube video player"  allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share" allowFullScreen ></iframe>
        </div>
      </div>
      </section>
  );
}
