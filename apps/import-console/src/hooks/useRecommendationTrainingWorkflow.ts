import { useCallback, useEffect, useRef, useState } from "react";
import type {
  QueueRecommendationTrainingRunRequest,
  RecommendationReviewContext,
  RecommendationTrainingEvent,
  RecommendationTrainingRunDetail,
} from "@ascendany/sdk";
import {
  loadRecommendationReviewContext,
  loadRecommendationTrainingEvents,
  loadRecommendationTrainingRun,
  queueRecommendationTraining,
  RecommendationWorkflowError,
} from "../api/recommendation";
import { apiFailureMessage } from "../api/v2Client";

const POLL_INTERVAL_MILLISECONDS = 1_000;

export type RecommendationWorkflowIssue =
  | { kind: "drift"; message: string; details: Record<string, unknown> | null }
  | { kind: "validation"; message: string; details: Record<string, unknown> | null }
  | { kind: "request"; message: string; details: null };

export function recommendationWorkflowIssue(error: unknown): RecommendationWorkflowIssue {
  if (error instanceof RecommendationWorkflowError) {
    if (error.status === 409 && error.apiError.code === "recommendation_analytics_head_conflict") {
      return {
        kind: "drift",
        message: "Analytics head 已发生变化，必须重新加载 recommendation review 后再继续。",
        details: error.apiError.details ?? null,
      };
    }
    if (error.status === 422) {
      return {
        kind: "validation",
        message: error.apiError.message,
        details: error.apiError.details ?? null,
      };
    }
  }
  return { kind: "request", message: apiFailureMessage(error), details: null };
}

function isTerminal(status: RecommendationTrainingRunDetail["status"]): boolean {
  return status === "succeeded" || status === "superseded" || status === "failed";
}

export interface RecommendationTrainingWorkflow {
  review: RecommendationReviewContext | null;
  run: RecommendationTrainingRunDetail | null;
  events: RecommendationTrainingEvent[];
  queueCreated: boolean | null;
  reviewIssue: RecommendationWorkflowIssue | null;
  queueIssue: RecommendationWorkflowIssue | null;
  trackingIssue: RecommendationWorkflowIssue | null;
  trackingStopped: boolean;
  loadingReview: boolean;
  queueing: boolean;
  polling: boolean;
  reloadReview(): Promise<void>;
  queue(body: QueueRecommendationTrainingRunRequest): Promise<void>;
  retryTracking(): void;
}

export function useRecommendationTrainingWorkflow(): RecommendationTrainingWorkflow {
  const [review, setReview] = useState<RecommendationReviewContext | null>(null);
  const [run, setRun] = useState<RecommendationTrainingRunDetail | null>(null);
  const [events, setEvents] = useState<RecommendationTrainingEvent[]>([]);
  const [queueCreated, setQueueCreated] = useState<boolean | null>(null);
  const [reviewIssue, setReviewIssue] = useState<RecommendationWorkflowIssue | null>(null);
  const [queueIssue, setQueueIssue] = useState<RecommendationWorkflowIssue | null>(null);
  const [trackingIssue, setTrackingIssue] = useState<RecommendationWorkflowIssue | null>(null);
  const [trackingStopped, setTrackingStopped] = useState(false);
  const [loadingReview, setLoadingReview] = useState(false);
  const [queueing, setQueueing] = useState(false);
  const [polling, setPolling] = useState(false);
  const [trackedRunId, setTrackedRunId] = useState<string | null>(null);
  const [trackingAttempt, setTrackingAttempt] = useState(0);
  const eventCursor = useRef(0);
  const trackingEpoch = useRef(0);

  const reloadReview = useCallback(async () => {
    setLoadingReview(true);
    setReviewIssue(null);
    try {
      const context = await loadRecommendationReviewContext();
      setReview(context);
    } catch (error) {
      setReview(null);
      setReviewIssue(recommendationWorkflowIssue(error));
    } finally {
      setLoadingReview(false);
    }
  }, []);

  const queue = useCallback(async (body: QueueRecommendationTrainingRunRequest) => {
    setQueueing(true);
    setQueueIssue(null);
    setQueueCreated(null);
    try {
      const result = await queueRecommendationTraining(body);
      trackingEpoch.current += 1;
      eventCursor.current = 0;
      setEvents([]);
      setQueueCreated(result.created);
      setRun({ ...result.trainingRun, failure: null });
      setTrackedRunId(result.trainingRun.id);
      setTrackingIssue(null);
      setTrackingStopped(false);
      setTrackingAttempt((current) => current + 1);
    } catch (error) {
      const nextIssue = recommendationWorkflowIssue(error);
      if (nextIssue.kind === "drift") setReview(null);
      setQueueIssue(nextIssue);
    } finally {
      setQueueing(false);
    }
  }, []);

  useEffect(() => {
    if (trackedRunId === null) return;
    const epoch = trackingEpoch.current;
    let cancelled = false;
    let timer: number | undefined;
    const stale = () => cancelled || trackingEpoch.current !== epoch;

    const poll = async () => {
      if (stale()) return;
      setPolling(true);
      setTrackingStopped(false);
      try {
        let afterSequence = eventCursor.current;
        const detail = await loadRecommendationTrainingRun(trackedRunId);
        if (stale()) return;
        const firstPage = await loadRecommendationTrainingEvents(trackedRunId, afterSequence);
        if (stale()) return;
        const nextEvents = [...firstPage.items];
        let nextCursor = firstPage.nextAfterSequence;
        if (nextEvents.length > 0) {
          afterSequence = nextEvents[nextEvents.length - 1]!.sequence;
        }
        while (nextCursor !== null) {
          const page = await loadRecommendationTrainingEvents(trackedRunId, nextCursor);
          if (stale()) return;
          nextEvents.push(...page.items);
          if (page.items.length > 0) {
            afterSequence = page.items[page.items.length - 1]!.sequence;
          }
          nextCursor = page.nextAfterSequence;
        }
        if (stale()) return;
        eventCursor.current = afterSequence;
        if (nextEvents.length > 0) {
          setEvents((current) => [...current, ...nextEvents]);
        }
        setRun(detail);
        setTrackingIssue(null);
        setTrackingStopped(false);
        if (!isTerminal(detail.status)) {
          timer = window.setTimeout(() => void poll(), POLL_INTERVAL_MILLISECONDS);
        }
      } catch (error) {
        if (!stale()) {
          setTrackingIssue(recommendationWorkflowIssue(error));
          setTrackingStopped(true);
        }
      } finally {
        if (!stale()) setPolling(false);
      }
    };

    void poll();
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [trackedRunId, trackingAttempt]);

  return {
    review,
    run,
    events,
    queueCreated,
    reviewIssue,
    queueIssue,
    trackingIssue,
    trackingStopped,
    loadingReview,
    queueing,
    polling,
    reloadReview,
    queue,
    retryTracking: () => {
      setTrackingStopped(false);
      setTrackingAttempt((current) => current + 1);
    },
  };
}
