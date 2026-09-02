+ [router] Serve LocalStack's `/_aws/ses` (GET, DELETE) and `/_aws/sqs/messages` inspection endpoints, so test assertions carried over from LocalStack pass.
  `/_aws/ses` lists captured emails in LocalStack's `{"messages": [...]}` shape from the same inbox as `/_overcast/ses/inbox/messages`, with `?id=` and `?email=` filters; DELETE clears them
  `/_aws/sqs/messages` peeks a queue without consuming it, as an SQS `ReceiveMessageResponse` in XML or JSON per `Accept`, honouring `ShowInvisible` and `ShowDelayed`
  the rest of `/_aws/` answers 404 naming the Overcast endpoint that has the data, instead of an S3 error
