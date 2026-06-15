export type SourceType = "episode" | "upload";
export type ProcessingJobType = "transcribe" | "analyze";
export type ProcessingJobStatus = "queued" | "running" | "succeeded" | "failed";
export type SourceProcessingStatus =
  | "unprocessed"
  | "queued"
  | "transcribing"
  | "transcribed"
  | "analyzing"
  | "processed"
  | "failed";

export interface TranscribeJobMessage {
  type: "processing.job";
  jobType: "transcribe";
  jobId: string;
  userId: string;
  sourceType: SourceType;
  sourceId: string;
}

export interface AnalyzeJobMessage {
  type: "processing.job";
  jobType: "analyze";
  jobId: string;
  userId: string;
  sourceType: SourceType;
  sourceId: string;
}

export type ProcessingQueueMessage = TranscribeJobMessage | AnalyzeJobMessage;
