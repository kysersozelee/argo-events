/*
Copyright 2018 BlackRock, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package amqp

import (
	"fmt"

	"github.com/argoproj/argo-events/gateways"
	amqplib "github.com/streadway/amqp"
)

// StartEventSource starts an event source
func (ese *AMQPEventSourceExecutor) StartEventSource(eventSource *gateways.EventSource, eventStream gateways.Eventing_StartEventSourceServer) error {
	ese.Log.Info().Str("event-stream-name", *eventSource.Name).Msg("operating on event source")
	a, err := parseEventSource(eventSource.Data)
	if err != nil {
		return err
	}

	dataCh := make(chan []byte)
	errorCh := make(chan error)
	doneCh := make(chan struct{}, 1)

	go ese.listenEvents(a, eventSource, dataCh, errorCh, doneCh)

	return gateways.HandleEventsFromEventSource(eventSource.Name, eventStream, dataCh, errorCh, doneCh, &ese.Log)
}

func getDelivery(ch *amqplib.Channel, a *amqp) (<-chan amqplib.Delivery, error) {
	err := ch.ExchangeDeclare(a.ExchangeName, a.ExchangeType, true, false, false, false, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to declare exchange with name %s and type %s. err: %+v", a.ExchangeName, a.ExchangeType, err)
	}

	q, err := ch.QueueDeclare("", false, false, true, false, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to declare queue: %s", err)
	}

	err = ch.QueueBind(q.Name, a.RoutingKey, a.ExchangeName, false, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to bind %s exchange '%s' to queue with routingKey: %s: %s", a.ExchangeType, a.ExchangeName, a.RoutingKey, err)
	}

	delivery, err := ch.Consume(q.Name, "", true, false, false, false, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin consuming messages: %s", err)
	}
	return delivery, nil
}

func (ese *AMQPEventSourceExecutor) listenEvents(a *amqp, eventSource *gateways.EventSource, dataCh chan []byte, errorCh chan error, doneCh chan struct{}) {
	defer gateways.Recover(eventSource.Name)

	conn, err := amqplib.Dial(a.URL)
	if err != nil {
		errorCh <- err
		return
	}

	ch, err := conn.Channel()
	if err != nil {
		errorCh <- err
		return
	}

	delivery, err := getDelivery(ch, a)
	if err != nil {
		errorCh <- err
		return
	}

	ese.Log.Info().Str("event-source-name", *eventSource.Name).Msg("starting to subscribe to messages")
	for {
		select {
		case msg := <-delivery:
			dataCh <- msg.Body
		case <-doneCh:
			err = conn.Close()
			if err != nil {
				ese.Log.Error().Err(err).Str("event-stream-name", *eventSource.Name).Msg("failed to close connection")
			}
			return
		}
	}
}
