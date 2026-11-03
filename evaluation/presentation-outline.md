# Results presentation — working outline

English content to match the report; confirm the teaching group's language if needed.
Target: 12 minutes of prepared material, with room within 15 minutes for explanation.
No new PPTX with invented results: create the final deck after collecting the data.

| Slide | Time | Content and evidence |
|---|---|---|
| 1. HLCP and the research question | 0:45 | One practical problem, research question, measured scope |
| 2. Controller–agent architecture | 1:00 | One diagram, local API vs mTLS connection, real topology |
| 3. Protocol and command lifecycle | 1:15 | Identity, resource/action allowlist, stable ID, result/unknown outcome |
| 4. Implemented system | 1:00 | Compact CLI example and one real UI screenshot; demo optional |
| 5. Experimental method | 1:30 | Same container/user/work, three connection modes, timing boundary, repetitions |
| 6. Command latency | 1:30 | Real distributions, sample sizes, failures, fresh and reused SSH |
| 7. CPU and memory | 1:00 | Actual idle/load values with units and accounting boundary |
| 8. Recovery and rejected requests | 1:30 | Timeline plus selected expected/observed cases; counter evidence |
| 9. Interpretation and limitations | 1:00 | Explain measured costs and benefits; one host pair, no usability/security overclaim |
| 10. Conclusion | 0:30 | Answer research question, repository/data link, remaining work |

Total: 11:00. Allow about one extra minute for slide transitions or a short demo;
the prepared talk should stay below 15 minutes even if the demo fails.

Prepare backup material: exact versions/topology, failure definitions, complete
policy matrix, raw-data permalink, and the crash window around external side effects.

Likely questions: Why an application protocol instead of SSH/Ansible? Why CBOR?
What does mTLS protect? How did SSH multiplexing affect the comparison? What happens
when the connection fails after execution? Is running equivalent to healthy? What
was measured rather than merely demonstrated by a test?
