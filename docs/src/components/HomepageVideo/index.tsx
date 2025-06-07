import React, { useState, useEffect } from 'react';
import clsx from 'clsx';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

type VideoItem = {
  title: string;
  description: string;
  embedId: string;
  duration?: string;
};

const videos: VideoItem[] = [
  {
    title: 'Advanced interLink Deployment Strategies',
    description: 'Deep dive into advanced deployment patterns and best practices for production interLink installations across diverse computing environments.',
    embedId: 'vTg58Nd7_58',
    duration: '20 min'
  },
  {
    title: 'interLink Plugin Development Workshop',
    description: 'Hands-on workshop demonstrating how to develop custom plugins for interLink to integrate with new compute backends and middleware systems.',
    embedId: 'bIxw1uK0QRQ',
    duration: '35 min'
  },
  {
    title: 'Cloud-Native HPC with interLink',
    description: 'Explore how interLink enables cloud-native approaches to high-performance computing workloads, bridging traditional HPC and modern container orchestration.',
    embedId: '0OTINz_4ORc',
    duration: '18 min'
  },
  {
    title: 'interLink Overview at KubeCon AI Day',
    description: 'Comprehensive overview of interLink presented at KubeCon colocated CloudNative AI Day, covering architecture, use cases, and real-world implementations.',
    embedId: 'M3uLQiekqo8',
    duration: '25 min'
  },
  {
    title: 'SLURM Integration with EuroHPC',
    description: 'Learn how interLink bridges Kubernetes with SLURM batch systems at EuroHPC supercomputing centers, enabling seamless hybrid cloud-HPC workflows.',
    embedId: '-djIQGPvYdI',
    duration: '15 min'
  },
  {
    title: 'Production Use Cases - Live Discussion',
    description: 'Live panel discussion with production users sharing their experiences, challenges, and success stories deploying interLink at scale.',
    embedId: 'enM7Y938P4k',
    duration: 'Live'
  },
  {
    title: 'interLink Community Meetup - Live Session', 
    description: 'Interactive community session featuring live demonstrations, Q&A, and discussions about the latest interLink developments and roadmap.',
    embedId: '2gagS3RzLzw',
    duration: 'Live'
  }
];

function VideoCard({ title, description, embedId, duration }: VideoItem) {
  return (
    <div className={styles.videoCard}>
      <div className={styles.videoWrapper}>
        <iframe 
          src={`https://www.youtube.com/embed/${embedId}?si=_enablejsapi=1&rel=0`}
          title={title}
          className={styles.videoIframe}
          allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share" 
          allowFullScreen
          loading="lazy"
        />
      </div>
      <div className={styles.videoContent}>
        <div className={styles.videoHeader}>
          <h3 className={styles.videoTitle}>{title}</h3>
          {duration && <span className={styles.videoDuration}>{duration}</span>}
        </div>
        <p className={styles.videoDescription}>{description}</p>
      </div>
    </div>
  );
}

export default function HomepageVideo(): JSX.Element {
  const [currentIndex, setCurrentIndex] = useState(0);
  const [isAutoPlay, setIsAutoPlay] = useState(true);
  const [isMobile, setIsMobile] = useState(false);

  // Responsive hook
  useEffect(() => {
    const checkMobile = () => {
      setIsMobile(window.innerWidth <= 996);
    };
    
    checkMobile();
    window.addEventListener('resize', checkMobile);
    return () => window.removeEventListener('resize', checkMobile);
  }, []);

  const videosPerView = isMobile ? 1 : 2;
  const maxIndex = Math.max(0, videos.length - videosPerView);

  useEffect(() => {
    if (!isAutoPlay) return;
    
    const interval = setInterval(() => {
      setCurrentIndex((prevIndex) => 
        prevIndex >= maxIndex ? 0 : prevIndex + 1
      );
    }, 10000); // 10 seconds for multiple videos

    return () => clearInterval(interval);
  }, [isAutoPlay, maxIndex]);

  // Reset currentIndex when screen size changes to avoid out-of-bounds
  useEffect(() => {
    setCurrentIndex(0);
  }, [videosPerView]);

  const goToSlide = (index: number) => {
    setCurrentIndex(Math.min(index, maxIndex));
    setIsAutoPlay(false);
    setTimeout(() => setIsAutoPlay(true), 20000); // Resume auto-play after 20 seconds
  };

  const nextSlide = () => {
    setCurrentIndex((prevIndex) => 
      prevIndex >= maxIndex ? 0 : prevIndex + 1
    );
    setIsAutoPlay(false);
    setTimeout(() => setIsAutoPlay(true), 20000);
  };

  const prevSlide = () => {
    setCurrentIndex((prevIndex) => 
      prevIndex <= 0 ? maxIndex : prevIndex - 1
    );
    setIsAutoPlay(false);
    setTimeout(() => setIsAutoPlay(true), 20000);
  };

  const visibleVideos = videos.slice(currentIndex, currentIndex + videosPerView);
  const totalSlides = maxIndex + 1;

  return (
    <div className={styles.videoSlider}>
      <div className={styles.sliderContainer}>
        <div className={styles.videoContent}>
          <div className={styles.videoGrid}>
            {visibleVideos.map((video, idx) => (
              <VideoCard key={currentIndex + idx} {...video} />
            ))}
          </div>
        </div>

        {totalSlides > 1 && (
          <>
            <button 
              className={clsx(styles.sliderButton, styles.prevButton)} 
              onClick={prevSlide}
              aria-label="Previous videos"
              disabled={currentIndex === 0}
            >
              ‹
            </button>
            <button 
              className={clsx(styles.sliderButton, styles.nextButton)} 
              onClick={nextSlide}
              aria-label="Next videos"
              disabled={currentIndex >= maxIndex}
            >
              ›
            </button>
          </>
        )}
      </div>

      {totalSlides > 1 && (
        <>
          <div className={styles.sliderDots}>
            {Array.from({ length: totalSlides }).map((_, index) => (
              <button
                key={index}
                className={clsx(styles.dot, index === currentIndex && styles.activeDot)}
                onClick={() => goToSlide(index)}
                aria-label={`Go to slide ${index + 1}`}
              />
            ))}
          </div>

          <div className={styles.videoCounter}>
            Showing {currentIndex + 1}-{Math.min(currentIndex + videosPerView, videos.length)} of {videos.length} videos
          </div>
        </>
      )}
    </div>
  );
}
