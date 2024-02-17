import Heading from '@theme/Heading';
import styles from './styles.module.css';
import ThemedImage from '@theme/ThemedImage';
import useBaseUrl from '@docusaurus/useBaseUrl';



export default function HomepageSchema(): JSX.Element {
  return (
    <section className={styles.features}>
    <div className="container">
          <div style={{textAlign: 'center'}}>
          <Heading as="h1">
          Translate your pod request into a remote lifecycle command set
        </Heading>
        <Heading as="h2">
          You just have to select a (virtual)node as the pod destination.
        </Heading>
              <ThemedImage className={styles.featureSvg}
        alt="Docusaurus themed image"
        sources={{
          light: useBaseUrl('/img/InterLink_excalidraw_light.svg'),
          dark: useBaseUrl('/img/InterLink_excalidraw-dark.svg'),
        }}
      />
        </div>
      </div>
      </section>
  );
}
