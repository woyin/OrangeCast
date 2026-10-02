// Run in the authenticated agent-browser page using eval --stdin.
// Set window.V02 = {question, source, article, session} from the live fixture.
// Before running, enter note/reflection/next-question drafts and start real audio.
(async () => {
  const f = window.V02;
  if (!f) throw Error('Live fixture identity is required');
  const audio = document.getElementById('audio-player');
  if (!audio || audio.paused) throw Error('Start actual playback first');
  const note = document.getElementById('listening-note-content').value;
  const reflection = document.getElementById('reflection-remember').value;
  const next = document.querySelector('#question-study-ask textarea[name=input]').value;
  const study = `/questions/${f.question}/study?session=${f.session}`;
  const routes = ['/questions', `/questions/${f.question}/gaps`, `/questions/${f.question}/understandings`, `/knowledge-articles/${f.article}`, `/knowledge-articles/${f.article}?revision=1`, '/quality-cases', `/learning-exports?kind=question&id=${f.question}`, '/search', `/sources/episode/${f.source}`, study];
  const rows = [];
  for (const route of routes) {
    await CWPNavigation.visit(route);
    await new Promise(resolve => setTimeout(resolve, 150));
    const row = {route, actual: location.pathname + location.search, sameAudio: document.getElementById('audio-player') === audio, paused: audio.paused, time: audio.currentTime, note: document.getElementById('listening-note-content').value, reflection: document.getElementById('reflection-remember').value};
    rows.push(row);
    if (row.actual !== route || !row.sameAudio || row.paused || row.note !== note || row.reflection !== reflection) throw Error(JSON.stringify(row));
  }
  if (document.querySelector('#question-study-ask textarea[name=input]').value !== next) throw Error('Next-question draft was lost');
  return rows;
})();
