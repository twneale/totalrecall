#!/usr/bin/env python3
"""
Random Forest Shell Command Predictor

This tool connects to Elasticsearch, retrieves shell command history,
trains a random forest model, and predicts the next command a user might execute.
"""

import os
import re
import sys
import json
import logging
import argparse
from datetime import datetime, timedelta
from collections import Counter, defaultdict

import numpy as np
import pandas as pd
from sklearn.ensemble import RandomForestClassifier
from sklearn.feature_extraction.text import CountVectorizer
from sklearn.model_selection import train_test_split
from sklearn.pipeline import Pipeline
from sklearn.metrics import accuracy_score
from elasticsearch import Elasticsearch
from elasticsearch.helpers import scan

# Set up logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger('shell_predictor')

class ElasticsearchConnector:
    """Handles connection to Elasticsearch and retrieval of shell history."""
    
    def __init__(self, host='localhost', port=9200, index='totalrecall', 
                 username=None, password=None, use_ssl=False):
        """Initialize Elasticsearch connection parameters."""
        self.index = index
        self.es_params = {
            'hosts': [f"{host}:{port}"],
        }
        
        if username and password:
            self.es_params['http_auth'] = (username, password)
        
        if use_ssl:
            self.es_params['use_ssl'] = True
            self.es_params['verify_certs'] = True
            
        self.client = None
        
    def connect(self):
        """Establish connection to Elasticsearch."""
        try:
            self.client = Elasticsearch(**self.es_params)
            if not self.client.ping():
                logger.error("Failed to connect to Elasticsearch")
                return False
            logger.info("Successfully connected to Elasticsearch")
            return True
        except Exception as e:
            logger.error(f"Error connecting to Elasticsearch: {e}")
            return False
    
    def get_command_history(self, days=30, max_commands=10000):
        """
        Retrieve command history from Elasticsearch.
        
        Args:
            days: Number of days of history to retrieve
            max_commands: Maximum number of commands to retrieve
            
        Returns:
            List of command records
        """
        if not self.client:
            if not self.connect():
                return []
        
        # Query for commands from the last N days
        start_date = datetime.now() - timedelta(days=days)
        start_date_str = start_date.strftime("%Y-%m-%dT%H:%M:%S")
        
        query = {
            "query": {
                "bool": {
                    "must": [
#                      {"term": {"pwd.keyword": os.getcwd()}},
                      {"match": {"return_code": 0}},
                    ],
                }
            },
            "sort": [
                {"@timestamp": {"order": "asc"}}
            ]
        }
        try:
            results = []
            for doc in scan(
                client=self.client,
                index=self.index,
                query=query,
                size=1000,  # Batch size for each scroll
            ):
                if '_source' in doc:
                    results.append(doc['_source'])
                if len(results) >= max_commands:
                    break
                    
            logger.info(f"Retrieved {len(results)} commands from Elasticsearch")
            return results
        except Exception as e:
            logger.error(f"Error retrieving command history: {e}")
            return []

    def get_recent_commands(self, count=20):
        """Get the most recent commands for prediction purposes."""
        if not self.client:
            if not self.connect():
                return []
        
        query = {
            "query": {
                "bool": {
                    "must": [
                      {"term": {"pwd.keyword": os.getcwd()}},
                      {"match": {"return_code": 0}},
                    ],
                }
            },
            "sort": [
                {"@timestamp": {"order": "desc"}}
            ],
            "size": count
        }
        
        try:
            response = self.client.search(
                index=self.index,
                body=query
            )
            
            recent_commands = []
            for hit in response['hits']['hits']:
                if '_source' in hit and 'command' in hit['_source']:
                    recent_commands.append(hit['_source'])
                    
            # Reverse to get chronological order
            recent_commands.reverse()
            return recent_commands
        except Exception as e:
            logger.error(f"Error retrieving recent commands: {e}")
            return []


class FeatureExtractor:
    """Extracts features from command history for model training."""
    
    def __init__(self):
        """Initialize feature extraction parameters."""
        self.command_vectorizer = CountVectorizer(
            analyzer='char_wb',
            ngram_range=(2, 5),
            max_features=200
        )
        self.common_commands = []
        self.hour_weights = {}
        self.day_weights = {}
        self.prev_cmd_weights = {}
        self.cwd_weights = {}
    
    def extract_command_parts(self, command):
        """
        Extract command name and arguments from a full command string.
        
        Args:
            command: Full command string
            
        Returns:
            Tuple of (command_name, args_list)
        """
        parts = []
        current_part = ""
        in_quotes = False
        quote_char = None
        
        for char in command.strip():
            if char in ['"', "'"]:
                if not in_quotes:
                    in_quotes = True
                    quote_char = char
                elif char == quote_char:
                    in_quotes = False
                    quote_char = None
                current_part += char
            elif char.isspace() and not in_quotes:
                if current_part:
                    parts.append(current_part)
                    current_part = ""
            else:
                current_part += char
                
        if current_part:
            parts.append(current_part)
            
        if not parts:
            return "", []
            
        return parts[0], parts[1:]
    
    def preprocess_data(self, command_history):
        """
        Preprocess command history for feature extraction.
        
        Args:
            command_history: List of command records from Elasticsearch
            
        Returns:
            DataFrame with preprocessed features
        """
        logger.info("Preprocessing command history data")
        
        if not command_history:
            logger.error("No command history to process")
            return pd.DataFrame()
        
        # Extract relevant data from each record
        processed_records = []
        previous_cmd = ""
        
        for i, record in enumerate(command_history):
            if 'command' not in record:
                continue
                
            cmd = record['command']
            timestamp = record.get('@timestamp', '')
            
            # Skip empty commands
            if not cmd or cmd.strip() == '':
                previous_cmd = cmd
                continue
                
            # Parse timestamp
            dt = None
            try:
                if timestamp:
                    dt = datetime.fromisoformat(timestamp.replace('Z', '+00:00'))
            except Exception:
                # If timestamp parsing fails, use a default
                dt = datetime.now()
            
            # Extract command name and args
            cmd_name, args = self.extract_command_parts(cmd)
            
            # Get current working directory from PWD env var if available
            cwd = None
            if 'env' in record and 'OLDPWD' in record['env']:
                cwd = record['env']['OLDPWD']
                # Extract just the last directory name
                if cwd:
                    cwd = os.path.basename(cwd)
            
            # Build record
            processed_record = {
                'command': cmd,
                'command_name': cmd_name,
                'num_args': len(args),
                'hour': dt.hour if dt else 0,
                'day_of_week': dt.weekday() if dt else 0,
                'previous_cmd': previous_cmd,
                'cwd': cwd if cwd else '',
                'return_code': record.get('return_code', 0)
            }
            #import pprint; pprint.pprint(processed_record)
            #import pdb; pdb.set_trace()
            
            processed_records.append(processed_record)
            previous_cmd = cmd
        
        # Convert to DataFrame
        df = pd.DataFrame(processed_records)
        
        # Calculate command frequency
        cmd_counts = Counter(df['command_name'])
        self.common_commands = [cmd for cmd, count in cmd_counts.most_common(100)]
        
        # Calculate time-based weights
        hour_counts = Counter(df['hour'])
        total_hours = sum(hour_counts.values())
        self.hour_weights = {hour: count/total_hours for hour, count in hour_counts.items()}
        
        day_counts = Counter(df['day_of_week'])
        total_days = sum(day_counts.values())
        self.day_weights = {day: count/total_days for day, count in day_counts.items()}
        
        # Calculate previous command weights
        prev_cmd_pairs = list(zip(df['previous_cmd'], df['command']))
        cmd_transitions = defaultdict(Counter)
        
        for prev, curr in prev_cmd_pairs:
            if prev and curr:
                prev_name, _ = self.extract_command_parts(prev)
                curr_name, _ = self.extract_command_parts(curr)
                cmd_transitions[prev_name][curr_name] += 1
        
        self.prev_cmd_weights = {}
        for prev_cmd, next_cmds in cmd_transitions.items():
            total = sum(next_cmds.values())
            self.prev_cmd_weights[prev_cmd] = {cmd: count/total for cmd, count in next_cmds.items()}
        
        # Calculate cwd weights
        if 'cwd' in df.columns:
            cwd_cmd_pairs = list(zip(df['cwd'], df['command']))
            cwd_transitions = defaultdict(Counter)
            
            for cwd, cmd in cwd_cmd_pairs:
                if cwd and cmd:
                    cmd_name, _ = self.extract_command_parts(cmd)
                    cwd_transitions[cwd][cmd_name] += 1
            
            self.cwd_weights = {}
            for cwd, cmds in cwd_transitions.items():
                total = sum(cmds.values())
                self.cwd_weights[cwd] = {cmd: count/total for cmd, count in cmds.items()}
        
        logger.info(f"Processed {len(processed_records)} command records")
        return df
    
    def fit_transform(self, command_history):
        """
        Fit on command history and transform to training data.
        
        Args:
            command_history: List of command records from Elasticsearch
            
        Returns:
            X: Feature matrix
            y: Target labels (next commands)
        """
        df = self.preprocess_data(command_history)
        if df.empty:
            return None, None
        
        # Extract commands and fit vectorizer
        commands = df['command'].values
        X_cmd_features = self.command_vectorizer.fit_transform(commands)
        
        # Create features for each command
        X_features = []
        y_labels = []
        
        for i in range(len(df) - 1):
            # Current command features
            cmd_features = X_cmd_features[i].toarray()[0]
            
            # Time features
            hour = df.iloc[i]['hour']
            day = df.iloc[i]['day_of_week']
            hour_weight = self.hour_weights.get(hour, 0)
            day_weight = self.day_weights.get(day, 0)
            
            # Command name in common commands?
            cmd_name = df.iloc[i]['command_name']
            is_common = 1 if cmd_name in self.common_commands else 0
            
            # Number of arguments
            num_args = df.iloc[i]['num_args']
            
            # Add all features
            features = list(cmd_features) + [hour_weight, day_weight, is_common, num_args]
            X_features.append(features)
            
            # Next command is the label
            y_labels.append(df.iloc[i+1]['command'])
        
        return np.array(X_features), np.array(y_labels)
    
    def transform(self, commands):
        """
        Transform a sequence of commands to features for prediction.
        
        Args:
            commands: List of recent command records
            
        Returns:
            Feature vector for latest command
        """
        if not commands:
            return None
        
        # Process commands like in preprocess_data but for a single sequence
        processed_commands = []
        previous_cmd = ""
        
        for record in commands:
            if 'command' not in record:
                continue
                
            cmd = record['command']
            timestamp = record.get('@timestamp', '')
            
            # Skip empty commands
            if not cmd or cmd.strip() == '':
                previous_cmd = cmd
                continue
                
            # Parse timestamp
            dt = None
            try:
                if timestamp:
                    dt = datetime.fromisoformat(timestamp.replace('Z', '+00:00'))
            except Exception:
                dt = datetime.now()
            
            # Extract command name and args
            cmd_name, args = self.extract_command_parts(cmd)
            
            # Get current working directory from PWD env var if available
            cwd = None
            if 'env' in record and 'PWD' in record['env']:
                cwd = record['env']['PWD']
                # Extract just the last directory name
                if cwd:
                    cwd = os.path.basename(cwd)
            
            # Build record
            processed_record = {
                'command': cmd,
                'command_name': cmd_name,
                'num_args': len(args),
                'hour': dt.hour if dt else 0,
                'day_of_week': dt.weekday() if dt else 0,
                'previous_cmd': previous_cmd,
                'cwd': cwd if cwd else '',
                'return_code': record.get('return_code', 0)
            }
            
            processed_commands.append(processed_record)
            previous_cmd = cmd
        
        if not processed_commands:
            return None
        
        # Get the latest command for prediction
        latest = processed_commands[-1]
        
        # Vectorize the command
        X_cmd_features = self.command_vectorizer.transform([latest['command']])
        cmd_features = X_cmd_features.toarray()[0]
        
        # Time features
        hour = latest['hour']
        day = latest['day_of_week']
        hour_weight = self.hour_weights.get(hour, 0)
        day_weight = self.day_weights.get(day, 0)
        
        # Command name in common commands?
        cmd_name = latest['command_name']
        is_common = 1 if cmd_name in self.common_commands else 0
        
        # Number of arguments
        num_args = latest['num_args']
        
        # Combine features
        features = list(cmd_features) + [hour_weight, day_weight, is_common, num_args]
        return np.array(features).reshape(1, -1)


class ShellPredictor:
    """Predicts shell commands using a random forest model."""
    
    def __init__(self, es_connector, n_estimators=100, max_depth=None):
        """
        Initialize the shell command predictor.
        
        Args:
            es_connector: ElasticsearchConnector instance
            n_estimators: Number of trees in the random forest
            max_depth: Maximum depth of trees
        """
        self.es_connector = es_connector
        self.feature_extractor = FeatureExtractor()
        self.model = RandomForestClassifier(
            n_estimators=n_estimators,
            max_depth=max_depth,
            random_state=42
        )
        self.is_trained = False
    
    def train(self, training_days=30, max_commands=10000):
        """
        Train the random forest model on historical command data.
        
        Args:
            training_days: Days of history to use for training
            max_commands: Maximum commands to use for training
            
        Returns:
            Training accuracy or None if training failed
        """
        logger.info(f"Training on {training_days} days of command history")
        
        # Get command history from Elasticsearch
        history = self.es_connector.get_command_history(
            days=training_days,
            max_commands=max_commands
        )
        
        if not history:
            logger.error("No command history retrieved for training")
            return None
        
        # Create features and labels
        X, y = self.feature_extractor.fit_transform(history)
        
        if X is None or y is None:
            logger.error("Feature extraction failed")
            return None
        
        # Split into training and test sets
        X_train, X_test, y_train, y_test = train_test_split(
            X, y, test_size=0.2, random_state=42
        )
        
        # Train the model
        try:
            self.model.fit(X_train, y_train)
            self.is_trained = True
            
            # Evaluate on test set
            y_pred = self.model.predict(X_test)
            accuracy = accuracy_score(y_test, y_pred)
            logger.info(f"Model trained with accuracy: {accuracy:.4f}")
            
            return accuracy
        except Exception as e:
            logger.error(f"Error training model: {e}")
            return None
    
    def predict_next_command(self, num_predictions=5):
        """
        Predict the next command based on recent command history.
        
        Args:
            num_predictions: Number of predictions to return
            
        Returns:
            List of tuples (command, probability)
        """
        if not self.is_trained:
            logger.error("Model not trained yet")
            return []
        
        # Get recent commands from Elasticsearch
        recent_commands = self.es_connector.get_recent_commands(count=20)
        
        if not recent_commands:
            logger.error("No recent commands retrieved for prediction")
            return []
        
        # Transform recent commands to features
        X = self.feature_extractor.transform(recent_commands)
        
        if X is None:
            logger.error("Feature transformation failed")
            return []
        
        # Get class probabilities
        try:
            # Get probability estimates
            proba = self.model.predict_proba(X)[0]
            
            # Get top N predictions
            top_indices = proba.argsort()[-num_predictions:][::-1]
            
            # Map indices to class labels (commands) and probabilities
            predictions = []
            for idx in top_indices:
                cmd = self.model.classes_[idx]
                prob = proba[idx]
                predictions.append((cmd, prob))
            
            return predictions
        except Exception as e:
            logger.error(f"Error making prediction: {e}")
            return []


def main():
    """Main entry point for the shell command predictor."""
    parser = argparse.ArgumentParser(
        description="Predict shell commands using a random forest model and Elasticsearch data"
    )
    
    # Elasticsearch connection parameters
    parser.add_argument('--host', default='localhost', help='Elasticsearch host')
    parser.add_argument('--port', type=int, default=9200, help='Elasticsearch port')
    parser.add_argument('--index', default='totalrecall', help='Elasticsearch index name')
    parser.add_argument('--username', help='Elasticsearch username (optional)')
    parser.add_argument('--password', help='Elasticsearch password (optional)')
    parser.add_argument('--ssl', action='store_true', help='Use SSL for Elasticsearch connection')
    
    # Model parameters
    parser.add_argument('--trees', type=int, default=100, help='Number of trees in random forest')
    parser.add_argument('--days', type=int, default=30, help='Days of history to use for training')
    parser.add_argument('--max-commands', type=int, default=10000, 
                      help='Maximum commands to use for training')
    parser.add_argument('--num-predictions', type=int, default=5, 
                      help='Number of command predictions to return')
    
    args = parser.parse_args()
    
    # Create Elasticsearch connector
    es_connector = ElasticsearchConnector(
        host=args.host,
        port=args.port,
        index=args.index,
        username=args.username,
        password=args.password,
        use_ssl=args.ssl
    )
    
    # Create and train the shell predictor
    predictor = ShellPredictor(
        es_connector=es_connector,
        n_estimators=args.trees
    )
    
    # Train the model
    accuracy = predictor.train(
        training_days=args.days,
        max_commands=args.max_commands
    )
    
    if accuracy is None:
        logger.error("Training failed")
        sys.exit(1)
    
    print(f"Model trained with accuracy: {accuracy:.4f}")
    
    # Predict next commands
    predictions = predictor.predict_next_command(
        num_predictions=args.num_predictions
    )
    
    if not predictions:
        logger.error("Prediction failed")
        sys.exit(1)
    
    # Display predictions
    print("\nPredicted next commands:")
    for i, (cmd, prob) in enumerate(predictions, 1):
        print(f"{i}. {cmd} (probability: {prob:.4f})")


if __name__ == "__main__":
    main()
