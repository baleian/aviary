// Asks the model of the session whether it takes pictures, which nothing
// else tells: it sends the model a word, and the same word with a small grey
// picture. A model that answers the first and refuses the second takes no
// pictures, and "refused" is printed. Otherwise nothing is printed: the
// model takes pictures, or it cannot be asked now.

// How long the model server may take to begin an answer, in ms. The harness
// starts once both answers have begun.
const WAIT = 20000;

// 64 by 64 pixels: a model may refuse a picture that is smaller than the
// parts that it cuts pictures into.
const PICTURE =
  "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAAAAACPAi4CAAAAKUlEQVR42u3MQREAAAwCIJtb3RD77SAA6VEEAoFAIBAIBAKBQCAQfA8G18/waiADHKgAAAAASUVORK5CYII=";

// takes tells whether the model server takes a message: it begins an answer,
// and not with an error. It fails if the server cannot be asked, or says
// that it has no time for the request.
async function takes(content) {
  const base = process.env.AVIARY_LLM_BASE_URL.replace(/\/+$/, "");
  const response = await fetch(base + "/chat/completions", {
    method: "POST",
    headers: {
      ...JSON.parse(process.env.AVIARY_LLM_HEADERS || "{}"),
      "Content-Type": "application/json",
      Authorization: "Bearer " + process.env.AVIARY_LLM_API_KEY,
    },
    body: JSON.stringify({
      model: process.env.AVIARY_LLM_MODEL,
      messages: [{ role: "user", content }],
      stream: true,
      max_tokens: 1,
    }),
    signal: AbortSignal.timeout(WAIT),
  });
  if (response.status === 429) throw new Error("the model server is busy");
  // The answer is not waited for beyond its beginning.
  const reader = response.body.getReader();
  const { value } = await reader.read();
  await reader.cancel();
  const begun = new TextDecoder().decode(value);
  return response.ok && !/^data: *\{"error"/m.test(begun);
}

const word = "hi";
const [alone, pictured] = await Promise.allSettled([
  takes(word),
  takes([
    { type: "text", text: word },
    { type: "image_url", image_url: { url: PICTURE } },
  ]),
]);
if (alone.value === true && pictured.value === false) console.log("refused");
